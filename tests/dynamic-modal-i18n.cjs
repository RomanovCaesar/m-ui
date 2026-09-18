// Run: node tests/dynamic-modal-i18n.cjs, then open the printed loopback URL.
// Uses the production dialog factories, translation catalog and openModal.
// No panel backend, WARP registration or remote subscription requests are used.
const http = require('node:http');
const fs = require('node:fs');
const path = require('node:path');
const web = path.join(__dirname, '..', 'web');
const read = name => fs.readFileSync(path.join(web, name), 'utf8');
function between(source, start, end) {
  const i = source.indexOf(start), j = source.indexOf(end, i);
  if (i < 0 || j < 0) throw Error('Production function boundary changed: ' + start);
  return source.slice(i, j);
}
const languages = ['zh-CN', 'zh-TW', 'en', 'ja', 'ru', 'fa', 'vi', 'es', 'tr', 'uk', 'pt-BR'];
function fixture(language, stored, legacy) {
  const open = legacy ? "function openModal(id){const modal=$('#'+id);if(modal)modal.classList.add('open');}" :
    between(read('index.html'), 'function openModal(id)', 'function closeModal(');
  const warp = between(read('mihomo-settings.js'), 'function ensureWarpModal()', 'let vpngateData=');
  const cross = between(read('cross-subscriptions.js'), 'const makeModal = () => {', 'const setError =');
  return `<!doctype html><html data-default-language="${language}"><meta charset="utf-8"><body>
<script>
// Keep the fixture's storage isolated from any panel preferences.
Object.defineProperty(window, 'localStorage', {value: {
  getItem: () => ${stored ? JSON.stringify(language) : 'null'}, setItem: () => {}
}});
</script><script src="/i18n-locales.js"></script><script src="/i18n.js"></script>
<script>
const $ = selector => document.querySelector(selector);
const SF = () => '<svg aria-hidden="true"></svg>';
let modal = null, warpBusy = false;
const close = () => {}, pull = () => {}, clearCache = () => {}, deleteWarp = () => {},
 addWarpOutbound = () => {}, syncWarpButtons = () => {};
${open}
${warp}
${cross}
window.addEventListener('load', () => {
  try {
    const assert = (ok, message) => { if (!ok) throw Error(message); };
    assert(muiGetAppliedLang() === ${JSON.stringify(language)}, 'Initial language');
    // Factories run AFTER the initial translation scan, exactly like a first click.
    ensureWarpModal(); makeModal();
    const note = document.querySelector('[data-i18n-html="mh.warpNote"]');
    assert(note.textContent.includes('注册'), 'Fixture must initially contain untranslated markup');
    const urls = $('#cs-urls'); urls.value = 'https://example.invalid/user-token';
    const createHandler = $('#warp-create').onclick, copyHandler = $('#cs-copy').onclick;
    for (const id of ['warp-modal', 'cross-subscriptions-modal']) {
      openModal(id);
      const root = $('#'+id);
      for (const el of root.querySelectorAll('[data-i18n]')) {
        const key = el.dataset.i18n;
        assert(muiT(key) !== key && el.textContent === muiT(key), id + ': ' + key);
      }
      for (const el of root.querySelectorAll('[data-i18n-html]')) {
        const expected = document.createElement('div'); expected.innerHTML = muiT(el.dataset.i18nHtml);
        assert(el.innerHTML === expected.innerHTML, id + ': ' + el.dataset.i18nHtml);
      }
      for (const [data, attr] of [['data-i18n-placeholder', 'placeholder'], ['data-i18n-aria', 'aria-label']]) {
        for (const el of root.querySelectorAll('['+data+']')) assert(el.getAttribute(attr) === muiT(el.getAttribute(data)), id + ': ' + attr);
      }
      root.classList.remove('open'); openModal(id);
    }
    assert(urls.value === 'https://example.invalid/user-token', 'User data changed');
    assert(createHandler === $('#warp-create').onclick && copyHandler === $('#cs-copy').onclick, 'Handlers replaced');
    assert(note.querySelector('a').getAttribute('href') === 'https://www.cloudflare.com/application/terms/', 'Terms link changed');
    parent.postMessage({ok:true}, location.origin);
  } catch (error) { parent.postMessage({ok:false, error:error.message}, location.origin); }
});
</script></body></html>`;
}
const runner = `<!doctype html><meta charset="utf-8"><title>Dynamic modal localization regression</title>
<h1>Dynamic modal localization regression</h1><p>Fresh page loads, no language reselection; all 11 supported languages.</p>
<button id="run">Run checks</button><pre id="results" role="status">Ready</pre>
<script>
document.querySelector('#run').onclick = async () => {
  const output = document.querySelector('#results'); output.textContent = '';
  let count = 0;
  async function run(language, stored, legacy=false) {
    const frame = document.createElement('iframe'); frame.hidden = true;
    const response = new Promise((resolve,reject) => {
      const timer = setTimeout(() => {window.removeEventListener('message', listener);reject(Error('Timed out'));}, 5000);
      function listener(event) {
        if(event.origin !== location.origin || event.source !== frame.contentWindow)return;
        clearTimeout(timer); window.removeEventListener('message', listener); resolve(event.data);
      }
      window.addEventListener('message',listener);
    });
    frame.src = '/case?lang='+language+'&stored='+Number(stored)+'&legacy='+Number(legacy);
    document.body.append(frame);
    try { return await response; } finally { frame.remove(); }
  }
  try {
    const baseline = await run('en', false, true);
    if(baseline.ok)throw Error('Regression no longer reproduces with the old opener');
    output.textContent += 'PASS: original implementation reproduces missing translation ('+baseline.error+')\\n';
    for (const language of ${JSON.stringify(languages)}) for (const stored of [false,true]) {
      const result = await run(language, stored);
      if(!result.ok)throw Error(language+': '+result.error);
      count++; output.textContent += 'PASS '+language+' / '+(stored?'stored preference':'server default')+'\\n';
    }
    output.textContent += 'ALL '+count+' COLD-LOAD CASES PASSED';
  } catch(error) { output.textContent += 'FAIL '+error.message; }
};
</script>`;
http.createServer((req, res) => {
  const url = new URL(req.url, 'http://localhost');
  res.setHeader('Cache-Control', 'no-store');
  if (['/i18n.js', '/i18n-locales.js'].includes(url.pathname)) {
    res.setHeader('Content-Type', 'text/javascript; charset=utf-8'); res.end(read(url.pathname.slice(1))); return;
  }
  res.setHeader('Content-Type', 'text/html; charset=utf-8');
  const language = url.searchParams.get('lang');
  if (url.pathname === '/case' && languages.includes(language)) res.end(fixture(language, url.searchParams.get('stored') === '1', url.searchParams.get('legacy') === '1'));
  else res.end(runner);
}).listen(21863, '127.0.0.1', () => console.log('Localization regression: http://127.0.0.1:21863/'));
