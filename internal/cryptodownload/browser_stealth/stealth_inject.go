package browserstealth

// Stealth injection scripts. These are injected into every page before it
// loads, to normalise browser fingerprint surfaces in headless environments.

// CanvasNoiseScript adds subtle pixel-level noise to Canvas toDataURL/toBlob
// outputs, preventing consistent canvas fingerprinting across sessions without
// materially changing the visual appearance.
const CanvasNoiseScript = `
(function() {
	const origToDataURL = HTMLCanvasElement.prototype.toDataURL;
	HTMLCanvasElement.prototype.toDataURL = function() {
		try {
			const ctx = this.getContext('2d');
			if (ctx && this.width > 0 && this.height > 0) {
				const img = ctx.getImageData(0, 0, this.width, this.height);
				for (let i = 0; i < img.data.length; i += 4) {
					img.data[i] ^= (i & 3);
				}
				ctx.putImageData(img, 0, 0);
			}
		} catch (_) {}
		return origToDataURL.apply(this, arguments);
	};
	const origToBlob = HTMLCanvasElement.prototype.toBlob;
	HTMLCanvasElement.prototype.toBlob = function(cb, type, quality) {
		try {
			const ctx = this.getContext('2d');
			if (ctx && this.width > 0 && this.height > 0) {
				const img = ctx.getImageData(0, 0, this.width, this.height);
				for (let i = 0; i < img.data.length; i += 4) {
					img.data[i] ^= (i & 3);
				}
				ctx.putImageData(img, 0, 0);
			}
		} catch (_) {}
		return origToBlob.apply(this, [cb, type, quality]);
	};
})();
`

// WebGLSpoofScript overrides WebGL getParameter to return consistent GPU
// vendor/renderer strings suitable for server environments without a physical
// GPU. These values mirror a typical Windows desktop configuration.
const WebGLSpoofScript = `
(function() {
	const getParam = WebGLRenderingContext.prototype.getParameter;
	WebGLRenderingContext.prototype.getParameter = function(p) {
		if (p === 37445) return 'Google Inc. (NVIDIA)';
		if (p === 37446) return 'ANGLE (NVIDIA, NVIDIA GeForce RTX 4060 Ti Direct3D11 vs_5_0 ps_5_0)';
		return getParam.call(this, p);
	};
	if (typeof WebGL2RenderingContext !== 'undefined') {
		const getParam2 = WebGL2RenderingContext.prototype.getParameter;
		WebGL2RenderingContext.prototype.getParameter = function(p) {
			if (p === 37445) return 'Google Inc. (NVIDIA)';
			if (p === 37446) return 'ANGLE (NVIDIA, NVIDIA GeForce RTX 4060 Ti Direct3D11 vs_5_0 ps_5_0)';
			return getParam2.call(this, p);
		};
	}
})();
`

// WebDriverHideScript removes the navigator.webdriver flag and related
// automation markers that headless Chrome exposes by default.
const WebDriverHideScript = `
(function() {
	Object.defineProperty(navigator, 'webdriver', { get: function() { return false; } });
	window.chrome = { runtime: {} };
	const origQuery = navigator.permissions.query.bind(navigator.permissions);
	navigator.permissions.query = function(params) {
		if (params.name === 'notifications') {
			return Promise.resolve({ state: Notification.permission });
		}
		return origQuery(params);
	};
	Object.defineProperty(navigator, 'plugins', { get: function() { return [1, 2, 3, 4, 5]; } });
	Object.defineProperty(navigator, 'languages', { get: function() { return ['zh-CN', 'zh', 'en']; } });
})();
`

// AllStealthScripts returns a concatenated string of all three stealth scripts
// for injection via Page.AddScriptToEvaluateOnNewDocument (CDP) or equivalent.
func AllStealthScripts() string {
	return CanvasNoiseScript + "\n" + WebGLSpoofScript + "\n" + WebDriverHideScript
}
