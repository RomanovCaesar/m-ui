/* m-ui optical material and interaction runtime. No framework or network assets.
 * Refraction uses the normal of a rounded-rectangle bevel and Snell's law.
 * The SVG filter receives the real composited backdrop, never a screenshot.
 */
(() => {
  'use strict';
  const NS = 'http://www.w3.org/2000/svg';
  const reducedMotion = matchMedia('(prefers-reduced-motion: reduce)');
  const reducedTransparency = matchMedia('(prefers-reduced-transparency: reduce)');
  const highContrast = matchMedia('(prefers-contrast: more), (forced-colors: active)');
  const chromium = /Chrome|Chromium|Edg\//.test(navigator.userAgent) && !/Firefox|OPR\//.test(navigator.userAgent);
  const canRefract = chromium && CSS.supports('backdrop-filter', 'url("#glass")');
  const surfaces = new Map();
  const bars = new Map();
  const maps = new Map();
  const surfaceSelector = '.sidebar,.topbar,.login-card,.modal-panel,.drawer,.general-menu,.auto-refresh-menu,.settings-menu,.top-actions .outline-btn,.top-actions .icon-btn,.inbounds-actions > .inbound-pill:not(#add-inbound),.sider-trigger';
  const barSelector = '.settings-tabbar,.mihomo-tabs,.inbound-status-tabs,.mihomo-editor-tabs,.nav';
  let defs, serial = 0, scanFrame = 0, opticsTimer = 0;
  const clamp = (x, min, max) => Math.max(min, Math.min(max, x));
  const motionOff = () => reducedMotion.matches || document.documentElement.dataset.themeAnimations === 'off';
  function svgNode(tag, attrs) {
    const node = document.createElementNS(NS, tag);
    for (const [key, value] of Object.entries(attrs || {})) node.setAttribute(key, value);
    return node;
  }

  // Map is capped in resolution; displacement distances remain in CSS pixels.
  // The center is optically flat and the rounded perimeter acts as a lens.
  function displacementMap(width, height, radius) {
    const ratio = Math.min(1, 640 / Math.max(width, height));
    const w = Math.max(2, Math.round(width * ratio)), h = Math.max(2, Math.round(height * ratio));
    const r = Math.min(radius * ratio, w / 2, h / 2);
    const key = `${w}:${h}:${Math.round(r * 10)}`;
    if (maps.has(key)) return maps.get(key);
    const canvas = document.createElement('canvas'); canvas.width = w; canvas.height = h;
    const ctx = canvas.getContext('2d');
    if (!ctx) return null;
    const pixels = ctx.createImageData(w, h);
    const bevel = Math.max(1, Math.min(16 * ratio, r * .7));
    const amplitude = Math.min(19, height * .24);
    const eta = 1 / 1.46;
    for (let y = 0; y < h; y++) {
      for (let x = 0; x < w; x++) {
        const px = x + .5 - w / 2, py = y + .5 - h / 2;
        const qx = Math.abs(px) - (w / 2 - r), qy = Math.abs(py) - (h / 2 - r);
        const ox = Math.max(qx, 0), oy = Math.max(qy, 0);
        const length = Math.hypot(ox, oy);
        const distance = length + Math.min(Math.max(qx, qy), 0) - r;
        let dx = 0, dy = 0;
        if (distance <= 0 && distance > -bevel) {
          const t = clamp(-distance / bevel, .015, 1);
          // A circular cap: steep at the rim, flat where it meets the center.
          const slope = (1 - t) / Math.sqrt(1 - (1 - t) ** 2);
          const incident = Math.atan(slope);
          const refracted = Math.asin(Math.sin(incident) * eta);
          const shift = Math.tan(incident - refracted) * amplitude * Math.sqrt(t);
          let nx, ny;
          if (length > .001) { nx = ox / length * Math.sign(px); ny = oy / length * Math.sign(py); }
          else { nx = qx > qy ? Math.sign(px) : 0; ny = qy >= qx ? Math.sign(py) : 0; }
          dx = -nx * shift; dy = -ny * shift;
        }
        const i = (y * w + x) * 4;
        pixels.data[i] = clamp(128 + dx / 48 * 255, 0, 255);
        pixels.data[i + 1] = clamp(128 + dy / 48 * 255, 0, 255);
        pixels.data[i + 2] = 128; pixels.data[i + 3] = 255;
      }
    }
    ctx.putImageData(pixels, 0, 0);
    const url = canvas.toDataURL();
    if (maps.size >= 48) maps.delete(maps.keys().next().value);
    maps.set(key, url);
    return url;
  }

  function removeFilter(state) {
    if (state.filter) state.filter.remove();
    state.filter = null; state.key = '';
    state.element.removeAttribute('data-lg-filter');
    state.element.style.removeProperty('--lg-filter');
  }
  function updateOptics() {
    opticsTimer = 0;
    // When the WebGL glass layer is active it draws the material behind every
    // surface, so the SVG displacement optics stand down (the interaction
    // layer — spring lens, press, keyboard — keeps running).
    const glActive = document.documentElement.getAttribute('data-glass-gl') === 'on';
    const baseOK = canRefract && !reducedTransparency.matches && !highContrast.matches;
    const enabled = baseOK && !glActive;
    document.documentElement.dataset.glassRefraction = glActive ? 'gl' : (enabled ? 'svg' : 'fallback');
    let count = 0;
    for (const state of surfaces.values()) {
      const el = state.element;
      // The sidebar nav pill sits behind nav button text/icons at z-index 1;
      // it should not use SVG displacement filters which cause glitching during
      // sidebar collapse/expand. Horizontal tabs use liquid-tabbar.js WebGL shaders.
      const isNavPill = el.classList.contains('liquid-pill-slider') || el.closest('.nav');
      const on = enabled && !isNavPill;
      const rect = el.getBoundingClientRect();
      const css = getComputedStyle(el);
      if (!on || !el.isConnected || css.visibility !== 'visible' || css.opacity === '0' || rect.width < 2 || rect.height < 2 || rect.bottom < 0 || rect.top > innerHeight || rect.right < 0 || rect.left > innerWidth || count++ >= 32) {
        removeFilter(state); continue;
      }
      const radius = Math.min(parseFloat(css.borderTopLeftRadius) || 16, rect.width / 2, rect.height / 2);
      // Use layout sizes, not transient scale from the pressure animation.
      const width = el.offsetWidth, height = el.offsetHeight;
      const key = `${width}:${height}:${radius}`;
      if (state.key === key) continue;
      const url = displacementMap(width, height, radius);
      if (!url) continue;
      if (!state.filter) {
        state.id = `mui-optical-${++serial}`;
        state.filter = svgNode('filter', { id: state.id, x: '0%', y: '0%', width: '100%', height: '100%', primitiveUnits: 'userSpaceOnUse', 'color-interpolation-filters': 'sRGB' });
        state.map = svgNode('feImage', { x: '0', y: '0', width: '100%', height: '100%', preserveAspectRatio: 'none', result: 'normalMap' });
        state.filter.append(state.map, svgNode('feDisplacementMap', { in: 'SourceGraphic', in2: 'normalMap', scale: '48', xChannelSelector: 'R', yChannelSelector: 'G' }));
        defs.append(state.filter);
      }
      state.map.setAttribute('href', url);
      state.map.setAttribute('width', width);
      state.map.setAttribute('height', height);
      state.key = key;
      el.style.setProperty('--lg-filter', `url("#${state.id}")`);
      el.setAttribute('data-lg-filter', '');
    }
  }
  function scheduleOptics() {
    clearTimeout(opticsTimer);
    opticsTimer = setTimeout(updateOptics, 100);
  }
  const resize = new ResizeObserver(scheduleOptics);
  const visibility = new IntersectionObserver(scheduleOptics);
  function surface(el) {
    if (surfaces.has(el)) return;
    const css = getComputedStyle(el);
    if (css.position === 'static') el.style.position = 'relative';
    if (css.zIndex === 'auto') el.style.zIndex = '0';
    el.classList.add('lg-surface');
    surfaces.set(el, { element: el }); resize.observe(el); visibility.observe(el);
  }

  function setupBar(bar) {
    if (bars.has(bar)) return;
    if (bar.classList.contains('has-liquid-tabbar') || bar.matches('.liquid-tabbar, .settings-tabbar, .mihomo-tabs, .inbound-status-tabs, .mihomo-editor-tabs')) return;
    const nav = bar.matches('.nav');
    const lens = document.createElement('span');
    lens.className = 'liquid-pill-slider'; lens.setAttribute('aria-hidden', 'true');
    bar.append(lens); surface(lens);
    const abort = new AbortController();
    const listen = (el, type, fn, options = {}) => el.addEventListener(type, fn, { ...options, signal: abort.signal });
    const buttons = () => Array.from(bar.querySelectorAll(nav ? 'button[data-view]' : 'button')).filter(b => !b.hidden && !b.disabled && b.getClientRects().length && b.offsetWidth > 0);
    const active = () => buttons().find(b => b.classList.contains('active') || b.getAttribute('aria-selected') === 'true') || buttons()[0];
    const coords = b => ({ x: b.offsetLeft, y: b.offsetTop, w: b.offsetWidth, h: b.offsetHeight });
    let value = null, target = null, velocity = { x: 0, y: 0, w: 0, h: 0 }, frame = 0, previous = 0;
    let pointer = null, dragging = false, suppressClickUntil = 0;
    // Press bulge — the indicator swells when grabbed/tapped, faithful to the
    // reference LiquidBottomTabs indicator (pressedScale). Critically damped so
    // it grows and settles without wobble; the axis stretch below adds the
    // liquid deformation as it slides.
    let press = 0, pressV = 0, pressTarget = 0, pulseTimer = 0;
    const K = 520, C = 2 * Math.sqrt(520);   // stiffness + critical damping (ζ=1, no overshoot)
    const PK = 900, PC = 2 * Math.sqrt(900); // press spring (snappier)
    const PRESS_SCALE = 1.22, STRETCH_CAP = .22;
    const draw = () => {
      if (!value) return;
      const off = motionOff();
      const speed = off ? 0 : Math.min(Math.abs(velocity[nav ? 'y' : 'x']) / 2600, STRETCH_CAP);
      const bulge = off ? 1 : 1 + (PRESS_SCALE - 1) * press;
      // Stretch along the axis of motion, squash across it (volume-ish preserved).
      const along = bulge * (1 + speed), across = bulge * (1 - speed * .68);
      lens.style.width = `${Math.max(1, value.w)}px`; lens.style.height = `${Math.max(1, value.h)}px`;
      lens.style.transform = `translate3d(${value.x}px,${value.y}px,0) scale(${nav ? across : along},${nav ? along : across})`;
    };
    function tick(time) {
      frame = 0;
      if (!bar.isConnected || !target) return;
      const dt = Math.min((time - (previous || time - 16)) / 1000, .032); previous = time;
      let settled = true;
      for (const key of ['x', 'y', 'w', 'h']) {
        velocity[key] += ((target[key] - value[key]) * K - velocity[key] * C) * dt;
        value[key] += velocity[key] * dt;
        if (Math.abs(target[key] - value[key]) > .08 || Math.abs(velocity[key]) > .2) settled = false;
      }
      pressV += ((pressTarget - press) * PK - pressV * PC) * dt;
      press += pressV * dt;
      if (Math.abs(pressTarget - press) > .004 || Math.abs(pressV) > .03) settled = false;
      if (settled || motionOff()) {
        value = { ...target }; velocity = { x: 0, y: 0, w: 0, h: 0 };
        press = pressTarget; pressV = 0; previous = 0;
      }
      draw();
      if (!settled && !motionOff()) frame = requestAnimationFrame(tick);
      else scheduleOptics();
    }
    function kick() { if (!frame && !motionOff()) { previous = 0; frame = requestAnimationFrame(tick); } }
    // A tap gives a brief swell-and-release; a drag holds the swell until release.
    function pulse() { clearTimeout(pulseTimer); pressTarget = 1; kick(); pulseTimer = setTimeout(() => { pressTarget = 0; kick(); }, 120); }
    function move(to, instant = false) {
      target = to;
      if (!value || instant || motionOff()) { value = { ...to }; velocity = { x: 0, y: 0, w: 0, h: 0 }; draw(); return; }
      if (!frame) { previous = 0; frame = requestAnimationFrame(tick); }
    }
    function sync(instant = false) {
      if (dragging) return;
      const b = active(); if (lens.hidden !== !b) lens.hidden = !b;
      for (const button of buttons()) {
        if (!nav) button.style.touchAction = button === b ? 'pan-y' : 'pan-x pan-y';
        if (nav) {
          if (button === b) button.setAttribute('aria-current', 'page');
          else button.removeAttribute('aria-current');
        }
      }
      if (b) move(coords(b), instant);
    }
    const ro = new ResizeObserver(() => sync(true)); ro.observe(bar);
    const mo = new MutationObserver(records => {
      if (records.some(r => r.target !== lens)) sync();
    });
    mo.observe(bar, { subtree: true, attributes: true, attributeFilter: ['class', 'aria-selected', 'hidden'], childList: true });
    listen(bar, 'click', e => {
      if (e.isTrusted && performance.now() < suppressClickUntil) { e.preventDefault(); e.stopImmediatePropagation(); }
    }, { capture: true });
    listen(bar, 'click', e => { if (e.target.closest('button')) pulse(); requestAnimationFrame(() => sync()); });
    listen(bar, 'keydown', e => {
      const keys = nav ? ['ArrowUp', 'ArrowDown'] : ['ArrowLeft', 'ArrowRight'];
      if (![...keys, 'Home', 'End'].includes(e.key) || !buttons().includes(e.target)) return;
      e.preventDefault();
      const list = buttons(), index = list.indexOf(e.target);
      const next = e.key === 'Home' ? 0 : e.key === 'End' ? list.length - 1 : (index + (e.key === keys[0] ? -1 : 1) + list.length) % list.length;
      list[next].focus({ preventScroll: true }); list[next].click();
      list[next].scrollIntoView({ block: 'nearest', inline: 'nearest', behavior: 'instant' });
    });
    if (!nav) {
      listen(bar, 'pointerdown', e => {
        if (e.button !== 0 || pointer || !e.isPrimary) return;
        const b = e.target.closest('button');
        // A touch drag on the selected lens moves the selection. Other tabs
        // retain native horizontal scrolling when the strip overflows.
        if (!b || !buttons().includes(b) || (e.pointerType === 'touch' && b !== active())) return;
        pointer = { id: e.pointerId, x: e.clientX, y: e.clientY, initial: coords(b), candidate: b };
      });
      listen(bar, 'pointermove', e => {
        if (!pointer || e.pointerId !== pointer.id) return;
        const dx = e.clientX - pointer.x, dy = e.clientY - pointer.y;
        if (!dragging) {
          if (Math.abs(dy) > Math.abs(dx) && Math.abs(dy) > 6) { pointer = null; return; }
          if (Math.abs(dx) < 5) return;
          dragging = true; lens.classList.add('is-dragging');
          bar.setPointerCapture(e.pointerId);
          clearTimeout(pulseTimer); pressTarget = 1; kick();
        }
        if (e.cancelable) e.preventDefault();
        const list = buttons(), first = coords(list[0]), last = coords(list[list.length - 1]);
        let x = pointer.initial.x + dx;
        const lower = first.x, upper = last.x + last.w - pointer.initial.w;
        if (x < lower) x = lower + (x - lower) * .18;
        if (x > upper) x = upper + (x - upper) * .18;
        const center = x + pointer.initial.w / 2;
        pointer.candidate = list.reduce((best, b) => Math.abs(coords(b).x + b.offsetWidth / 2 - center) < Math.abs(coords(best).x + best.offsetWidth / 2 - center) ? b : best, list[0]);
        move({ ...pointer.initial, x, w: pointer.candidate.offsetWidth });
      }, { passive: false });
      function finish(e, cancel) {
        if (!pointer || e.pointerId !== pointer.id) return;
        const chosen = pointer.candidate, wasDragging = dragging;
        pointer = null; dragging = false; lens.classList.remove('is-dragging');
        clearTimeout(pulseTimer); pressTarget = 0; kick();
        if (bar.hasPointerCapture(e.pointerId)) bar.releasePointerCapture(e.pointerId);
        if (wasDragging) { suppressClickUntil = performance.now() + 350; if (!cancel && chosen) chosen.click(); }
        sync();
      }
      listen(window, 'pointerup', e => finish(e, false));
      listen(window, 'pointercancel', e => finish(e, true));
      listen(bar, 'lostpointercapture', e => finish(e, true));
      listen(window, 'blur', () => { if (pointer) finish({ pointerId: pointer.id }, true); });
    }
    bars.set(bar, { sync, destroy() { abort.abort(); ro.disconnect(); mo.disconnect(); cancelAnimationFrame(frame); lens.remove(); } });
    sync(true);
  }

  function refresh() {
    if (!defs || scanFrame) return;
    scanFrame = requestAnimationFrame(() => {
      scanFrame = 0;
      for (const [el, state] of bars) if (!el.isConnected) { state.destroy(); bars.delete(el); }
      for (const [el, state] of surfaces) if (!el.isConnected) {
        removeFilter(state); resize.unobserve(el); visibility.unobserve(el); surfaces.delete(el);
      }
      document.querySelectorAll(surfaceSelector).forEach(surface);
      document.querySelectorAll(barSelector).forEach(setupBar);
      scheduleOptics();
    });
  }
  function init() {
    const svg = svgNode('svg', { width: '0', height: '0', 'aria-hidden': 'true', focusable: 'false' });
    svg.style.cssText = 'position:absolute;pointer-events:none;overflow:hidden';
    defs = svgNode('defs'); svg.append(defs); document.body.append(svg);
    const mo = new MutationObserver(records => {
      // Ignore lens style writes, SVG map updates and optical decoration.
      if (records.some(r => r.type === 'childList' && r.target.namespaceURI !== NS || r.type === 'attributes' && !r.target.matches('.liquid-pill-slider'))) refresh();
    });
    mo.observe(document.body, { subtree: true, childList: true, attributes: true, attributeFilter: ['class', 'hidden', 'open'] });
    let pointerFrame = 0, lastEvent;
    document.addEventListener('pointermove', e => {
      if (motionOff() || e.pointerType === 'touch') return;
      lastEvent = e;
      if (pointerFrame) return;
      pointerFrame = requestAnimationFrame(() => {
        pointerFrame = 0;
        const host = lastEvent.target.closest?.('.lg-surface');
        if (!host) return;
        const r = host.getBoundingClientRect();
        const angle = Math.atan2(lastEvent.clientY - r.top - r.height / 2, lastEvent.clientX - r.left - r.width / 2) * 180 / Math.PI;
        host.style.setProperty('--lg-angle', `${angle + 90}deg`);
      });
    }, { passive: true });
    let pressed = null;
    const release = () => { if (pressed) pressed.classList.remove('lg-pressed'); pressed = null; };
    document.addEventListener('pointerdown', e => {
      release();
      const b = e.target.closest?.('button.lg-surface:not(:disabled)');
      if (!b || motionOff()) return;
      pressed = b; b.classList.add('lg-pressed');
    });
    window.addEventListener('pointerup', release); window.addEventListener('pointercancel', release); window.addEventListener('blur', release);
    document.addEventListener('scroll', scheduleOptics, { passive: true, capture: true });
    window.addEventListener('resize', scheduleOptics, { passive: true });
    reducedTransparency.addEventListener('change', scheduleOptics); highContrast.addEventListener('change', scheduleOptics);
    reducedMotion.addEventListener('change', () => bars.forEach(b => b.sync(true)));
    document.addEventListener('visibilitychange', () => { if (!document.hidden) refresh(); });
    refresh();
  }
  window.MUILiquidGlass = Object.freeze({ refresh });
  if (document.readyState === 'loading') document.addEventListener('DOMContentLoaded', init, { once: true });
  else init();
})();
