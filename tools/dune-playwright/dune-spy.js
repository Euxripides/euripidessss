// Dune Full Spy — monitors ALL fetch/XHR, captures GraphQL + REST + headers + cookies.
// Paste into dune.com browser console (F12 → Console), then create/run a query.
(function() {
    const LOG_KEY = '__dune_spy__';
    window[LOG_KEY] = window[LOG_KEY] || [];
    const seen = new Set();

    function log(op, info) {
        const key = op + '|' + JSON.stringify(info.request || '');
        if (seen.has(key)) return;
        seen.add(key);
        const entry = { time: new Date().toISOString(), operation: op, ...info };
        window[LOG_KEY].push(entry);

        const badge = entry.status ? (entry.status < 300 ? '🟢' : '🔴') : '⚪';
        console.group(badge + ' ' + op + (entry.status ? ' [' + entry.status + ']' : ''));
        for (const [k, v] of Object.entries(info)) {
            if (v !== undefined && v !== null && v !== '') {
                console.log('%c' + k + ':', 'color:#ff0;font-weight:bold', v);
            }
        }
        console.groupEnd();
    }

    // --- Intercept fetch ---
    const origFetch = window.fetch;
    window.fetch = async function(url, opts = {}) {
        const urlStr = typeof url === 'string' ? url : url.url;
        const reqHeaders = opts.headers ? { ...opts.headers } : {};
        if (opts.headers instanceof Headers) {
            opts.headers.forEach((v, k) => reqHeaders[k] = v);
        }
        const reqBody = typeof opts.body === 'string' ? opts.body : null;

        const resp = await origFetch.apply(this, arguments);
        const clone = resp.clone();
        const respBody = await clone.text();

        const respHeaders = {};
        clone.headers.forEach((v, k) => respHeaders[k] = v);

        let op = urlStr.split('/').pop()?.split('?')[0] || urlStr;
        if (urlStr.includes('/api/')) op = 'REST ' + urlStr.split('/api/')[1]?.split('?')[0];
        if (urlStr.includes('/graphql')) {
            const q = new URL(urlStr, location.origin).searchParams.get('operationName');
            if (q) op = 'GQL ' + q;
        }

        log(op, {
            method: opts.method || 'GET',
            url: urlStr.split('?')[0],
            requestHeaders: reqHeaders,
            requestBody: safeJson(reqBody),
            responseHeaders: respHeaders,
            responseBody: safeJson(respBody),
            status: resp.status,
        });
        return resp;
    };

    // --- Intercept XHR ---
    const OrigXHR = window.XMLHttpRequest;
    window.XMLHttpRequest = function() {
        const xhr = new OrigXHR();
        const origOpen = xhr.open;
        const origSend = xhr.send;
        const origSetHeader = xhr.setRequestHeader;

        let method, urlStr;
        const reqHeaders = {};

        xhr.open = function(method_, url_) {
            method = method_;
            urlStr = typeof url_ === 'string' ? url_ : url_.toString();
            return origOpen.apply(xhr, arguments);
        };
        xhr.setRequestHeader = function(name, value) {
            reqHeaders[name] = value;
            return origSetHeader.apply(xhr, arguments);
        };
        xhr.send = function(body) {
            xhr.addEventListener('loadend', function() {
                let op = urlStr.split('/').pop()?.split('?')[0] || urlStr;
                if (urlStr.includes('/api/')) op = 'REST ' + urlStr.split('/api/')[1]?.split('?')[0];
                if (urlStr.includes('/graphql')) {
                    const q = new URL(urlStr, location.origin).searchParams.get('operationName');
                    if (q) op = 'GQL ' + q;
                }
                const respHeaders = {};
                const rh = xhr.getAllResponseHeaders();
                if (rh) {
                    rh.split('\r\n').forEach(l => {
                        const [k, v] = l.split(': ');
                        if (k) respHeaders[k] = v;
                    });
                }
                log(op, {
                    method: method || 'GET',
                    url: urlStr.split('?')[0],
                    requestHeaders: reqHeaders,
                    requestBody: safeJson(body),
                    responseHeaders: respHeaders,
                    responseBody: safeJson(xhr.responseText),
                    status: xhr.status,
                });
            });
            return origSend.apply(xhr, arguments);
        };
        return xhr;
    };
    window.XMLHttpRequest.prototype = OrigXHR.prototype;

    function safeJson(str) {
        if (!str) return null;
        try { return JSON.parse(str); } catch(e) { return str; }
    }

    // Also capture current cookies/tokens
    const cookies = document.cookie.split(';').map(c => c.trim()).filter(Boolean);
    const lsTokens = {};
    for (let i = 0; i < localStorage.length; i++) {
        const k = localStorage.key(i);
        if (k && (k.includes('token') || k.includes('Token') || k.includes('Cognito'))) {
            lsTokens[k] = localStorage.getItem(k);
        }
    }

    console.clear();
    console.log('%c Dune SPY Active ', 'background:#0f0;color:#000;font-size:16px;padding:10px');
    console.log('Cookies:', cookies);
    console.log('localStorage tokens:', lsTokens);
    console.log('');
    console.log('%cNow create/run a query on Dune — ALL requests will be logged.',
        'color:#ff0;font-size:14px');
    console.log('%cAfter done, run: %ccopy(JSON.stringify(window.__dune_spy__, null, 2))',
        'color:#fff', 'color:#0f0');
})();
