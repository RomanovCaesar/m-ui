/* Progressive enhancement: all application state stays on the original input.
   Selection checkboxes in tables and sync matrices are deliberately excluded. */
(() => {
  'use strict';
  const selector = '.switch > input[type="checkbox"],.theme-switch > input[type="checkbox"],.ultra-check > input[type="checkbox"],.check-box > input[type="checkbox"],.drawer .check > input[type="checkbox"],#client-modal .check > input[type="checkbox"]';
  const NS = 'http://www.w3.org/2000/svg';
  const reduced = matchMedia('(prefers-reduced-transparency: reduce), (prefers-contrast: more), (forced-colors: active)');
  const supports = /Chrome|Chromium|Edg\//.test(navigator.userAgent) && CSS.supports('backdrop-filter', 'url("#lt")');
  const upgraded = new WeakSet();
  const filters = new Map();
  let defs, pending = false, pointer = null, suppress = null;
  const clamp = v => Math.max(0, Math.min(1, v));
  function node(tag, attrs) {
    const el = document.createElementNS(NS, tag);
    for (const [k,v] of Object.entries(attrs)) el.setAttribute(k,v);
    return el;
  }
  // Only two shared lens maps for the entire document, independent of row count.
  // Rounded capsule normals encode a curved rim, with a flat neutral center.
  function lens(width, height) {
    const key = width + '-' + height;
    if (filters.has(key)) return filters.get(key);
    const canvas = document.createElement('canvas'); canvas.width = width * 2; canvas.height = height * 2;
    const ctx = canvas.getContext('2d'); if (!ctx) return '';
    const data = ctx.createImageData(canvas.width, canvas.height), radius = height / 2;
    for (let y = 0; y < canvas.height; y++) for (let x = 0; x < canvas.width; x++) {
      const px = (x + .5) / 2 - width / 2, py = (y + .5) / 2 - height / 2;
      const nx = Math.sign(px) * Math.max(0, Math.abs(px) - (width - height) / 2), ny = py;
      const len = Math.hypot(nx, ny), depth = radius - len;
      let shift = 0;
      if (depth > 0 && depth < 5) {
        const t = Math.max(.025, depth / 5), angle = Math.acos(t);
        shift = Math.tan(angle - Math.asin(Math.sin(angle) / 1.46)) * 3.8 * Math.sin(t * Math.PI);
      }
      const i = (y * canvas.width + x) * 4;
      data.data[i] = 128 - (len ? nx / len : 0) * shift / 12 * 255;
      data.data[i+1] = 128 - (len ? ny / len : 0) * shift / 12 * 255;
      data.data[i+2] = 128; data.data[i+3] = 255;
    }
    ctx.putImageData(data,0,0);
    const id = 'mui-toggle-lens-' + key;
    const filter = node('filter', {id, x:'0%', y:'0%', width:'100%', height:'100%', 'color-interpolation-filters':'sRGB'});
    filter.append(node('feImage', {href:canvas.toDataURL(), width:width-2, height:height-2, preserveAspectRatio:'none', result:'normal'}),
      node('feDisplacementMap', {in:'SourceGraphic', in2:'normal', scale:12, xChannelSelector:'R', yChannelSelector:'G'}));
    defs.append(filter); const value = `url("#${id}")`; filters.set(key,value); return value;
  }
  function decorate(shell, small) {
    shell.classList.add('lg-toggle'); shell.classList.toggle('lt-small', small);
    let track = shell.matches('button') ? null : shell.querySelector(':scope > span');
    if (!track) { track = document.createElement('span'); shell.append(track); }
    track.classList.add('lt-track'); track.setAttribute('aria-hidden','true');
    const thumb = document.createElement('i'); thumb.className = 'lt-thumb'; track.append(thumb);
    if (supports) shell.style.setProperty('--lt-filter', lens(small ? 32 : 40, small ? 22 : 28));
    shell.classList.toggle('lt-optical', supports && !reduced.matches);
  }
  function upgrade(input) {
    if (upgraded.has(input) || input.hidden || input.style.display === 'none') return;
    upgraded.add(input);
    let shell = input.parentElement;
    if (shell.matches('.check')) {
      shell = document.createElement('span'); input.before(shell); shell.append(input);
    }
    decorate(shell, shell.classList.contains('switch-sm'));
    input.setAttribute('role','switch');
    // Reuse the visible row label so translations update the accessible name too.
    if (!input.hasAttribute('aria-label') && !input.hasAttribute('aria-labelledby') && !Array.from(input.labels || []).some(l => l.textContent.trim())) {
      const row = shell.closest('.setting-row,.basics-row,.client-switch-row,.theme-option-row,.auto-refresh-row,.field');
      const label = row?.querySelector('strong,label:not(:has(input)),:scope > span');
      if (label && !label.contains(shell)) {
        if (!label.id) label.id = 'lt-label-' + (++upgrade.serial);
        input.setAttribute('aria-labelledby',label.id);
      }
    }
  }
  upgrade.serial = 0;
  function scan() {
    pending = false;
    document.querySelectorAll(selector).forEach(upgrade);
    document.querySelectorAll('button.search-toggle').forEach(button => {
      if (!upgraded.has(button)) { upgraded.add(button); decorate(button,false); button.setAttribute('role','switch'); }
      const checked = String(button.classList.contains('status-mode'));
      if (button.getAttribute('aria-checked') !== checked) button.setAttribute('aria-checked',checked);
    });
  }
  function queue() { if (!pending) { pending = true; queueMicrotask(scan); } }
  const control = shell => shell.querySelector(':scope > input') || shell;
  const isOn = el => el.matches('input') ? el.checked : el.classList.contains('status-mode');
  const disabled = el => el.matches(':disabled') || el.getAttribute('aria-disabled') === 'true';
  function finish(event, cancel) {
    if (!pointer || (event && event.pointerId !== pointer.id)) return;
    const p = pointer; pointer = null;
    p.shell.classList.remove('lt-press','lt-drag');
    p.shell.style.removeProperty('--lt-drag-x'); p.shell.style.removeProperty('--lt-drag-fill');
    if (p.shell.hasPointerCapture(p.id)) p.shell.releasePointerCapture(p.id);
    if (p.drag || cancel) suppress = {shell:p.shell, until:performance.now()+400};
    if (!cancel && p.drag && !disabled(p.input) && p.input.isConnected) {
      const next = p.fraction >= .5;
      // click() preserves application click/input/change handlers and cancellation.
      if (isOn(p.input) !== next) p.input.click();
    }
  }
  function init() {
    const svg = node('svg', {width:0,height:0,'aria-hidden':true,focusable:false});
    svg.style.cssText = 'position:absolute;pointer-events:none;overflow:hidden';
    defs = node('defs',{}); svg.append(defs); document.body.append(svg);
    scan();
    new MutationObserver(records => {
      if (records.some(r => r.type === 'childList' && r.target.namespaceURI !== NS || r.type === 'attributes' && r.target.matches('button.search-toggle'))) queue();
    }).observe(document.body,{childList:true,subtree:true,attributes:true,attributeFilter:['class']});
    document.addEventListener('click', e => {
      if (e.isTrusted && e.detail !== 0 && suppress && performance.now() < suppress.until && suppress.shell.contains(e.target)) { e.preventDefault(); e.stopImmediatePropagation(); }
    },true);
    document.addEventListener('pointerdown', e => {
      const shell = e.target.closest('.lg-toggle');
      if (!shell || pointer || e.button !== 0 || !e.isPrimary) return;
      const input = control(shell); if (disabled(input)) return;
      suppress = null; // A new gesture must never be swallowed after a drag.
      const css = getComputedStyle(shell), thumb = shell.querySelector('.lt-thumb');
      const travel = shell.clientWidth - thumb.offsetWidth - 4;
      pointer = {shell,input,id:e.pointerId,x:e.clientX,y:e.clientY,travel,rtl:css.direction==='rtl',initial:isOn(input)?1:0,fraction:isOn(input)?1:0,drag:false};
      shell.classList.add('lt-press');
    });
    document.addEventListener('pointermove', e => {
      const p = pointer; if (!p || e.pointerId !== p.id) return;
      if (disabled(p.input) || !p.input.isConnected) { finish(e,true); return; }
      const dx = e.clientX-p.x, dy = e.clientY-p.y;
      if (!p.drag) {
        if (Math.abs(dy)>5 && Math.abs(dy)>Math.abs(dx)) { finish(e,true); return; }
        if (Math.abs(dx)<4) return;
        p.drag = true; p.shell.classList.add('lt-drag'); p.shell.setPointerCapture(e.pointerId);
      }
      if(e.cancelable)e.preventDefault();
      p.fraction = clamp(p.initial + dx / p.travel * (p.rtl?-1:1));
      p.shell.style.setProperty('--lt-drag-x', `${(p.rtl?1-p.fraction:p.fraction)*p.travel}px`);
      p.shell.style.setProperty('--lt-drag-fill',p.fraction);
    },{passive:false});
    window.addEventListener('pointerup',e=>finish(e,false));
    window.addEventListener('pointercancel',e=>finish(e,true));
    document.addEventListener('lostpointercapture',e=>finish(e,true),true);
    window.addEventListener('blur',()=>finish(null,true));
    reduced.addEventListener('change',()=>document.querySelectorAll('.lg-toggle').forEach(el=>el.classList.toggle('lt-optical',supports&&!reduced.matches)));
  }
  if(document.readyState==='loading')document.addEventListener('DOMContentLoaded',init,{once:true}); else init();
})();
