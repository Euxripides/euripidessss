package dunetools

const rodPageBlockedJS = `() => {
	const text = document.body?.innerText || '';
	const title = document.title || '';
	return /Just a moment|Sorry, you have been blocked|enable cookies|Cloudflare|challenge|Turnstile|Verify you are human/i.test(text + ' ' + title);
}`

const rodLoggedInSurfaceJS = `() => {
	const text = document.body?.innerText || '';
	const url = location.href;
	return /\/home|\/queries|\/discover|\/dashboards/.test(url) || /My Queries|Discover|Dashboard|Create|New query/i.test(text);
}`

const rodClickTextJS = `(texts) => {
	const wanted = (Array.isArray(texts) ? texts : [texts]).map(t => String(t).toLowerCase());
	const nodes = Array.from(document.querySelectorAll('button,a,[role="button"]'));
	for (const text of wanted) {
		const node = nodes.find(el => (el.innerText || el.textContent || '').trim().toLowerCase().includes(text));
		if (node) {
			node.click();
			return true;
		}
	}
	return false;
}`

const rodFillLoginJS = `(email, password) => {
	const visible = el => !!(el && el.offsetParent !== null);
	const inputs = Array.from(document.querySelectorAll('input'));
	const emailInput = inputs.find(el => visible(el) && /username|email/i.test([el.autocomplete, el.type, el.name, el.placeholder].join(' ')));
	const passwordInput = inputs.find(el => visible(el) && el.type === 'password');
	if (!emailInput || !passwordInput) return false;
	for (const [el, value] of [[emailInput, email], [passwordInput, password]]) {
		el.focus();
		el.value = value;
		el.dispatchEvent(new Event('input', { bubbles: true }));
		el.dispatchEvent(new Event('change', { bubbles: true }));
	}
	return true;
}`

const rodFillUsernameJS = `(username) => {
	const text = document.body?.innerText || '';
	if (!/Choose a Dune username|provide a username/i.test(text)) return false;
	const input = Array.from(document.querySelectorAll('input')).find(el => /username|satoshi/i.test([el.autocomplete, el.name, el.placeholder].join(' ')));
	if (!input) return false;
	input.focus();
	input.value = username;
	input.dispatchEvent(new Event('input', { bubbles: true }));
	input.dispatchEvent(new Event('change', { bubbles: true }));
	const btn = Array.from(document.querySelectorAll('button')).find(el => /Continue|Next|Save|Submit/i.test(el.innerText || ''));
	if (btn) btn.click();
	return true;
}`

const rodAccessTokenJS = `() => {
	for (let i = 0; i < localStorage.length; i++) {
		const key = localStorage.key(i);
		if (key && key.includes('accessToken') && key.includes('Cognito')) return localStorage.getItem(key) || '';
	}
	return '';
}`

const rodTeamIDJS = `async (authorization) => {
	const res = await fetch('/public/graphql?operationName=GetTeams', {
		method: 'POST',
		headers: {'Content-Type': 'application/json', 'Authorization': authorization},
		body: JSON.stringify({operationName:'GetTeams',query:'query GetTeams{teams{edges{node{id}}}}',variables:{},extensions:{clientLibrary:{name:'@apollo/client',version:'4.1.6'}}}),
		credentials: 'include'
	});
	const json = await res.json();
	return json?.data?.teams?.edges?.[0]?.node?.id || 0;
}`
