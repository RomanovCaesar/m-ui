const assert = require('node:assert/strict');

const base = process.env.MUI_VPNGATE_TEST_URL || 'http://127.0.0.1:21540';
const cdpPort = process.env.MUI_CDP_PORT || '9227';

async function connectCDP() {
  const response = await fetch(`http://127.0.0.1:${cdpPort}/json/new?${encodeURIComponent(base + '/')}`, { method: 'PUT' });
  if (!response.ok) throw new Error(`Chrome DevTools endpoint returned ${response.status}`);
  const target = await response.json();
  const socket = new WebSocket(target.webSocketDebuggerUrl);
  await new Promise((resolve, reject) => {
    socket.addEventListener('open', resolve, { once: true });
    socket.addEventListener('error', reject, { once: true });
  });
  let sequence = 0;
  const pending = new Map();
  const listeners = new Map();
  socket.addEventListener('message', event => {
    const message = JSON.parse(event.data);
    if (message.id) {
      const waiter = pending.get(message.id);
      if (!waiter) return;
      pending.delete(message.id);
      if (message.error) waiter.reject(new Error(message.error.message));
      else waiter.resolve(message.result || {});
      return;
    }
    for (const listener of listeners.get(message.method) || []) listener(message.params || {});
  });
  const send = (method, params = {}) => new Promise((resolve, reject) => {
    const id = ++sequence;
    pending.set(id, { resolve, reject });
    socket.send(JSON.stringify({ id, method, params }));
  });
  const once = method => new Promise(resolve => {
    const listener = params => {
      listeners.set(method, (listeners.get(method) || []).filter(item => item !== listener));
      resolve(params);
    };
    listeners.set(method, [...(listeners.get(method) || []), listener]);
  });
  return { socket, send, once };
}

async function main() {
  const cdp = await connectCDP();
  const exceptions = [];
  cdp.listeners = exceptions;
  await cdp.send('Page.enable');
  await cdp.send('Runtime.enable');
  const exceptionListener = params => exceptions.push(params.exceptionDetails?.text || 'JavaScript exception');
  // connectCDP intentionally keeps its listener map private; console errors are
  // also checked from an in-page listener installed immediately after login.

  async function evaluate(expression, awaitPromise = true) {
    const result = await cdp.send('Runtime.evaluate', { expression, awaitPromise, returnByValue: true });
    if (result.exceptionDetails) throw new Error(result.exceptionDetails.exception?.description || result.exceptionDetails.text);
    return result.result?.value;
  }

  async function waitFor(expression, timeout = 8000) {
    const deadline = Date.now() + timeout;
    while (Date.now() < deadline) {
      if (await evaluate(`Boolean(${expression})`)) return;
      await new Promise(resolve => setTimeout(resolve, 50));
    }
    throw new Error(`Timed out waiting for: ${expression}`);
  }

  await waitFor(`document.readyState === 'complete'`);
  await waitFor(`document.querySelector('#login-form') || document.querySelector('#mihomo-settings-root')`);
  if (await evaluate(`Boolean(document.querySelector('#login-form'))`)) {
    await evaluate(`(() => {
      document.querySelector('#username').value = 'admin';
      document.querySelector('#password').value = 'admin';
      document.querySelector('#login-form').requestSubmit();
    })()`);
  }
  await waitFor(`document.querySelector('#mihomo-settings-root')`);

  await evaluate(`(() => {
    window.__qaErrors = [];
    window.addEventListener('error', event => window.__qaErrors.push(String(event.error || event.message)));
    window.addEventListener('unhandledrejection', event => window.__qaErrors.push(String(event.reason)));
    const realFetch = window.fetch.bind(window);
    window.fetch = async (input, init = {}) => {
      const url = new URL(typeof input === 'string' ? input : input.url, location.href);
      if (url.pathname === '/api/mihomo/vpngate' && (!init.method || init.method === 'GET')) {
        return new Response(JSON.stringify({ok:true,data:{
          install:{installed:true,running:false,supported:true,version:'v4.44-9807-rtm'},
          presets:[
            {code:'JP',keyword:true,isps:[{id:'kddi',label:'KDDI'},{id:'softbank',label:'SoftBank'},{id:'sony',label:'Sony'},{id:'docomo',label:'Docomo'},{id:'asahi',label:'Asahi'},{id:'chubu',label:'Chubu'}]},
            {code:'KR',keyword:false,isps:[{id:'kt',label:'KT'},{id:'sk',label:'SK'},{id:'lg',label:'LG'}]}
          ],maxSlot:9,statuses:[],usedSlots:[]
        }}), {status:200,headers:{'Content-Type':'application/json'}});
      }
      if (url.pathname === '/api/mihomo/vpngate/outbound' && init.method === 'POST') {
        const input = JSON.parse(init.body || '{}');
        const name = input.name || ('vpngate-' + input.country.toLowerCase() + '-' + input.slot);
        return new Response(JSON.stringify({ok:true,data:{
          kind:'proxy',name,type:'direct',interfaceName:'vpn_vpn'+input.slot,
          routingMark:100+Number(input.slot),ipVersion:'ipv4',
          vpngate:{country:input.country,isp:input.isp||'',ispKeyword:input.ispKeyword||'',slot:Number(input.slot)}
        }}), {status:200,headers:{'Content-Type':'application/json'}});
      }
      return realFetch(input, init);
    };
  })()`);

  await evaluate(`document.querySelector('[data-view="mihomo"]').click()`);
  await evaluate(`document.querySelector('[data-mihomo-tab="outbounds"]').click()`);
  const hadOldFixture = await evaluate(`(() => {
    const row=[...document.querySelectorAll('#mihomo-outbound-table tbody tr')].find(item=>item.textContent.includes('qa-vpngate'));
    if(!row)return false;
    window.confirm=()=>true;
    row.querySelector('[data-outbound-delete]').click();
    document.querySelector('#mihomo-save').click();
    return true;
  })()`);
  if (hadOldFixture) await waitFor(`document.querySelector('#mihomo-save').disabled`);
  await evaluate(`document.querySelector('#mihomo-vpngate').click()`);
  await waitFor(`document.querySelector('#vpngate-modal.open') && !document.querySelector('#vpngate-form').hidden`);
  let formState = await evaluate(`(() => { const f=document.querySelector('#vpngate-form'); return {ispDisabled:f.elements.isp.disabled,addDisabled:document.querySelector('#vpngate-add').disabled}; })()`);
  assert.deepEqual(formState, { ispDisabled: true, addDisabled: true });

  await evaluate(`(() => { const f=document.querySelector('#vpngate-form'); f.elements.country.value='jp'; f.elements.country.dispatchEvent(new Event('input',{bubbles:true})); })()`);
  formState = await evaluate(`(() => { const f=document.querySelector('#vpngate-form'); return {country:f.elements.country.value,ispDisabled:f.elements.isp.disabled,preview:document.querySelector('#vpngate-slot-preview').textContent}; })()`);
  assert.equal(formState.country, 'JP');
  assert.equal(formState.ispDisabled, false);
  assert.match(formState.preview, /vpn_vpn0/);
  assert.match(formState.preview, /100/);

  await evaluate(`(() => { const f=document.querySelector('#vpngate-form'); f.elements.isp.value='SoftBank'; f.elements.isp.dispatchEvent(new Event('input',{bubbles:true})); f.elements.country.value='kr'; f.elements.country.dispatchEvent(new Event('input',{bubbles:true})); })()`);
  formState = await evaluate(`(() => { const f=document.querySelector('#vpngate-form'); return {country:f.elements.country.value,isp:f.elements.isp.value,addDisabled:document.querySelector('#vpngate-add').disabled,note:document.querySelector('#vpngate-isp-note').textContent}; })()`);
  assert.equal(formState.country, 'KR');
  assert.equal(formState.isp, '');
  assert.equal(formState.addDisabled, true);
  assert.match(formState.note, /KT|韩国|Korea/i);

  await evaluate(`(() => { const f=document.querySelector('#vpngate-form'); f.elements.isp.value='custom'; f.elements.isp.dispatchEvent(new Event('input',{bubbles:true})); })()`);
  assert.equal(await evaluate(`document.querySelector('#vpngate-add').disabled`), true);
  await evaluate(`(() => { const f=document.querySelector('#vpngate-form'); f.elements.isp.value='KT'; f.elements.isp.dispatchEvent(new Event('input',{bubbles:true})); f.elements.slot.value='9'; f.elements.slot.dispatchEvent(new Event('change',{bubbles:true})); f.elements.name.value='qa-vpngate'; })()`);
  formState = await evaluate(`({addDisabled:document.querySelector('#vpngate-add').disabled,preview:document.querySelector('#vpngate-slot-preview').textContent})`);
  assert.equal(formState.addDisabled, false);
  assert.match(formState.preview, /vpn_vpn9/);
  assert.match(formState.preview, /109/);

  await evaluate(`document.querySelector('#vpngate-form').requestSubmit()`);
  await waitFor(`!document.querySelector('#vpngate-modal').classList.contains('open')`);
  let outboundText = await evaluate(`document.querySelector('#mihomo-outbound-table').textContent`);
  assert.match(outboundText, /qa-vpngate/);
  assert.match(outboundText, /VPNGate/);
  assert.match(outboundText, /vpn_vpn9/);
  assert.equal(await evaluate(`document.querySelector('#mihomo-save').disabled`), false);

  await evaluate(`document.querySelector('#mihomo-save').click()`);
  await waitFor(`document.querySelector('#mihomo-save').disabled`);
  const generated = await evaluate(`fetch('/api/raw-config').then(r=>r.json()).then(r=>r.data.config)`);
  assert.match(generated, /name: "qa-vpngate"/);
  assert.match(generated, /interface-name: "vpn_vpn9"/);
  assert.match(generated, /routing-mark: 109/);
  assert.match(generated, /ip-version: "ipv4"/);

  await evaluate(`document.querySelector('[data-outbound-edit]').click()`);
  try {
    await waitFor(`document.querySelector('#vpngate-modal.open') && document.querySelector('#vpngate-form').elements.country.value === 'KR'`);
  } catch (error) {
    console.error('edit-debug', await evaluate(`(() => { const f=document.querySelector('#vpngate-form'); return {url:location.href,hidden:f?.hidden,country:f?.elements.country.value,isp:f?.elements.isp.value,slot:f?.elements.slot.value,install:document.querySelector('#vpngate-install-message')?.textContent,error:document.querySelector('#vpngate-error')?.textContent,outbounds:document.querySelector('#mihomo-outbound-table')?.textContent}; })()`));
    throw error;
  }
  formState = await evaluate(`(() => { const f=document.querySelector('#vpngate-form'); return {country:f.elements.country.value,isp:f.elements.isp.value,slot:f.elements.slot.value}; })()`);
  assert.deepEqual(formState, { country: 'KR', isp: 'KT', slot: '9' });

  await cdp.send('Emulation.setDeviceMetricsOverride', { width: 390, height: 844, deviceScaleFactor: 1, mobile: false });
  await evaluate(`applyTheme(true)`);
  const layout = await evaluate(`(() => { const r=document.querySelector('.vpngate-modal').getBoundingClientRect(); return {left:r.left,right:r.right,width:r.width,viewport:innerWidth,overflow:document.querySelector('.vpngate-modal').scrollWidth>document.querySelector('.vpngate-modal').clientWidth,dark:document.body.classList.contains('dark')}; })()`);
  assert.equal(layout.dark, true);
  assert.equal(layout.overflow, false);
  assert.ok(layout.left >= 0 && layout.right <= layout.viewport, JSON.stringify(layout));
  await cdp.send('Emulation.clearDeviceMetricsOverride');
  await evaluate(`document.querySelector('#vpngate-close').click()`);

  // Remove the isolated test outbound again, exercising the normal dependency
  // check/delete/save path without touching the user's real panel data.
  await cdp.send('Page.setDownloadBehavior', { behavior: 'deny' });
  await cdp.send('Page.enable');
  await evaluate(`window.confirm=()=>true; document.querySelector('[data-outbound-delete]').click()`);
  await evaluate(`document.querySelector('#mihomo-save').click()`);
  await waitFor(`document.querySelector('#mihomo-save').disabled`);
  outboundText = await evaluate(`document.querySelector('#mihomo-outbound-table').textContent`);
  assert.doesNotMatch(outboundText, /qa-vpngate/);

  await evaluate(`document.querySelector('[data-view="settings"]').click()`);
  await waitFor(`document.querySelector('#settings-form [name="ipinfoToken"]')`);
  await evaluate(`(() => { const input=document.querySelector('#settings-form [name="ipinfoToken"]'); input.value='qa-token'; input.dispatchEvent(new Event('input',{bubbles:true})); document.querySelector('#save-settings').click(); })()`);
  await waitFor(`!document.querySelector('#ipinfo-token-status').hidden && document.querySelector('#settings-form [name="ipinfoToken"]').value === ''`);
  let settings = await evaluate(`fetch('/api/state').then(r=>r.json()).then(r=>r.data.state.settings)`);
  assert.equal(settings.ipinfoToken, undefined);
  assert.equal(settings.ipinfoTokenSet, true);
  assert.equal(await evaluate(`document.querySelector('#settings-form [name="ipinfoToken"]').value`), '');
  assert.equal(await evaluate(`document.querySelector('#ipinfo-token-status').hidden`), false);

  await evaluate(`document.querySelector('#clear-ipinfo-token').click(); document.querySelector('#save-settings').click()`);
  await new Promise(resolve => setTimeout(resolve, 300));
  settings = await evaluate(`fetch('/api/state').then(r=>r.json()).then(r=>r.data.state.settings)`);
  assert.equal(settings.ipinfoToken, undefined);
  assert.equal(settings.ipinfoTokenSet, undefined);

  const pageErrors = await evaluate(`window.__qaErrors`);
  assert.deepEqual(pageErrors, []);
  console.log('PASS: VPNGate modal linkage, country/ISP rules, slot 0/9 previews, draft/save/edit/delete, managed config, Token masking/clear, dark and narrow layout');
  cdp.socket.close();
}

main().catch(error => {
  console.error(error);
	process.exit(1);
});
