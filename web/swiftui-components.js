/* ===== m-ui Apple SwiftUI & Liquid Glass 交互系统 =====
   专为 Apple SwiftUI & iOS 26 / macOS 26 (Tahoe) Liquid Glass 打造的微交互体系：
   1. Liquid Glass 动态游标光学追踪 (Real-time Specular Highlight Tracker)
   2. SwiftUI Picker 下拉选择器与 Searchable Combobox 增强器
   3. iOS 26 / macOS 26 Dynamic Island 灵动浮岛消息 (Interactive Toasts)
   4. SwiftUI List / Sheet 触控弹性和弹簧物理过渡 */
(function(){
  'use strict';

  var STYLE_ID = 'mui-swiftui-components-style';
  var ARROW = '<svg class="sf-symbol mui-select-arrow" viewBox="64 64 896 896" aria-hidden="true"><path d="M884 256h-75c-5.1 0-9.9 2.5-12.9 6.6L512 651 227.9 262.6c-3-4.1-7.8-6.6-12.9-6.6h-75c-6.5 0-10.3 7.4-6.5 12.7l352.6 486.1c12.8 17.6 39 17.6 51.7 0l352.6-486.1c3.9-5.3.1-12.7-6.4-12.7z" fill="currentColor"></path></svg>';

  var CSS = [
    /* SwiftUI Picker 下拉选择器 */
    ':where(.swift-picker){position:relative;display:block;width:100%;min-width:0}',
    '.swift-picker .mui-native-select{position:absolute;top:0;left:0;width:1px;height:1px;margin:0;padding:0;border:0;opacity:0;pointer-events:none;appearance:none;-webkit-appearance:none}',
    '.swift-picker[hidden]{display:none}',
    /* .swift-picker-selection：Apple Squircle 连续曲线微边框选择容器 */
    '.swift-picker-selection{position:relative;display:flex;align-items:center;height:var(--ctl,34px);padding:0 28px 0 12px;border:1px solid var(--stroke,rgba(60,60,67,.18));border-radius:var(--radius-sm,10px);background:var(--select-bg,var(--surface,#fff));color:var(--text,rgba(0,0,0,.85));font-size:13px;font-weight:500;cursor:pointer;outline:none;user-select:none;-webkit-user-select:none;box-shadow:var(--shadow-btn),var(--glass-caustic);transition:border-color .2s var(--ease),background-color .2s var(--ease),box-shadow .2s var(--ease),transform .15s var(--ease)}',
    '.swift-picker-selection:hover{border-color:var(--teal-3,#1a88ff);background-color:var(--select-hover,rgba(0,122,255,.05))}',
    '.swift-picker-open .swift-picker-selection, .swift-picker:focus-visible .swift-picker-selection{border-color:var(--teal,#007aff);box-shadow:0 0 0 3px var(--teal-soft,rgba(0,122,255,.2)),var(--shadow-btn);outline:0}',
    '.swift-picker-disabled .swift-picker-selection{background:var(--surface-subtle,rgba(0,0,0,.04));color:var(--muted,rgba(0,0,0,.45));cursor:not-allowed;opacity:.55;box-shadow:none}',
    '.swift-picker-disabled .swift-picker-selection:hover{border-color:var(--stroke,rgba(60,60,67,.18));background:var(--surface-subtle,rgba(0,0,0,.04))}',
    '.mui-select-value{flex:1;min-width:0;overflow:hidden;line-height:32px;text-overflow:ellipsis;white-space:nowrap;letter-spacing:-0.01em}',
    /* 箭头：SF Symbol chevron down，展开平滑旋转 180° */
    '.mui-select-arrow{position:absolute;top:50%;right:11px;margin-top:-6px;width:12px;height:12px;color:var(--muted-2,rgba(60,60,67,.4));pointer-events:none;transition:transform .25s var(--ease),color .2s var(--ease)}',
    '.swift-picker-open .mui-select-arrow{transform:rotate(180deg);color:var(--teal,#007aff)}',
    /* .swift-picker-menu：macOS 26 Tahoe / iOS 26 Liquid Glass 浮层菜单 */
    '.swift-picker-menu{position:fixed;z-index:9000;margin:0;padding:6px;list-style:none;max-height:280px;overflow-y:auto;overflow-x:hidden;border:1px solid var(--stroke,rgba(60,60,67,.18));border-radius:var(--radius,14px);background:var(--surface-modal,rgba(255,255,255,.9));backdrop-filter:var(--glass-blur);-webkit-backdrop-filter:var(--glass-blur);box-shadow:var(--shadow-pop,0 16px 48px rgba(0,0,0,.16)),var(--glass-caustic);animation:swift-menu-in .18s cubic-bezier(0.16,1,0.3,1);transform-origin:top center}',
    '.swift-picker-menu[hidden]{display:none}',
    '@keyframes swift-menu-in{0%{opacity:0;transform:translateY(-8px) scale(.97)}100%{opacity:1;transform:translateY(0) scale(1)}}',
    /* 选项项：连续圆角、触感反馈、高亮 */
    '.swift-picker-menu li{position:relative;margin:2px 0;padding:7px 12px;border-radius:var(--radius-sm,8px);color:var(--select-item-color,var(--text,rgba(0,0,0,.85)));font-size:13px;font-weight:500;line-height:20px;cursor:pointer;transition:background-color .15s var(--ease),color .15s var(--ease);overflow:hidden;text-overflow:ellipsis;white-space:nowrap}',
    '.swift-picker-menu li:hover, .swift-picker-menu li.mui-active{background:var(--select-item-hover,rgba(0,122,255,.1))}',
    '.swift-picker-menu li.mui-selected{background:var(--select-item-selected,rgba(0,122,255,.14));color:var(--teal,#007aff);font-weight:600}',
    '.swift-picker-menu li:empty::after{content:"None";font-weight:400;color:var(--muted-2,rgba(60,60,67,.4))}',
    /* SwiftUI Combobox (可输入与即时过滤的下拉选择器) */
    ':where(.swift-combobox){position:relative;display:block;width:100%;min-width:0}',
    '.swift-combobox .swift-picker-selection{cursor:text}',
    '.swift-combobox .swift-combobox-input{flex:1;width:100%!important;height:100%!important;min-width:0!important;padding:0!important;margin:0!important;border:0!important;border-radius:0!important;outline:0!important;box-shadow:none!important;background:transparent!important;color:inherit!important;font:inherit!important;font-size:13px!important;line-height:32px!important;cursor:text}',
    '.swift-combobox:not(.swift-picker-disabled) .swift-picker-selection:hover{border-color:var(--teal-3,#1a88ff);background-color:var(--select-hover,rgba(0,122,255,.05))}',
    '.swift-combobox.swift-picker-open .swift-picker-selection, .swift-combobox:focus-within .swift-picker-selection{border-color:var(--teal,#007aff);box-shadow:0 0 0 3px var(--teal-soft,rgba(0,122,255,.2));outline:0}',
    '.swift-combobox.swift-picker-disabled .swift-picker-selection{background:var(--surface-subtle,rgba(0,0,0,.04));color:var(--muted,rgba(0,0,0,.45));cursor:not-allowed;border-color:var(--stroke,rgba(60,60,67,.18))}',
    '.swift-combobox.swift-picker-disabled .swift-combobox-input{cursor:not-allowed;color:var(--muted,rgba(0,0,0,.45))}',
    '.swift-combobox .mui-select-arrow{cursor:pointer;pointer-events:auto}',
    '.mui-combobox-item{display:flex;align-items:center;justify-content:space-between;gap:8px}',
    '.mui-combobox-sub{margin-left:auto;color:var(--muted,rgba(60,60,67,.5));font-size:12px;font-weight:400}'
  ].join('\n');

  function injectStyle(){
    if(document.getElementById(STYLE_ID)) return;
    var style = document.createElement('style');
    style.id = STYLE_ID;
    style.textContent = CSS;
    (document.head || document.documentElement).appendChild(style);
  }

  /* ===== 1. Liquid Glass 动态游标光学追踪器 ===== */
  function updateGlassCursor(e){
    var target = e.target.closest ? e.target.closest('.glass-surface, .glass-card, .swift-card, .glass-button, .swift-btn, .sidebar, .nav button, .swift-picker-selection') : null;
    if(!target) return;
    var rect = target.getBoundingClientRect();
    if(rect.width === 0 || rect.height === 0) return;
    var x = ((e.clientX - rect.left) / rect.width) * 100;
    var y = ((e.clientY - rect.top) / rect.height) * 100;
    target.style.setProperty('--glass-x', x.toFixed(1) + '%');
    target.style.setProperty('--glass-y', y.toFixed(1) + '%');
  }
  document.addEventListener('pointermove', updateGlassCursor, { passive: true });

  /* ===== 2. SwiftUI Dynamic Island 灵动浮岛消息系统 ===== */
  var currentToast = null;
  var toastTimer = null;

  window.dynamicIslandToast = function(msg, isError, duration){
    if(toastTimer){
      clearTimeout(toastTimer);
      toastTimer = null;
    }
    if(!currentToast){
      currentToast = document.createElement('div');
      currentToast.className = 'dynamic-island-toast';
      currentToast.setAttribute('role', 'status');
      document.body.appendChild(currentToast);
    }

    currentToast.className = 'dynamic-island-toast' + (isError ? ' is-error' : '');
    var iconName = isError ? 'close-circle' : 'check-circle';
    var iconHtml = window.SF ? window.SF(iconName, 'toast-icon') : (window.AI ? window.AI(iconName, 'toast-icon') : '');
    currentToast.innerHTML = (iconHtml ? '<span class="toast-icon-wrap">' + iconHtml + '</span>' : '') + '<span class="toast-text">' + String(msg || '') + '</span>';
    currentToast.style.display = 'flex';

    var dur = typeof duration === 'number' ? duration : (isError ? 4000 : 2600);
    toastTimer = setTimeout(function(){
      if(currentToast){
        currentToast.style.transition = 'opacity 0.25s var(--ease), transform 0.25s var(--ease)';
        currentToast.style.opacity = '0';
        currentToast.style.transform = 'translate(-50%, -20px) scale(0.9)';
        setTimeout(function(){
          if(currentToast && currentToast.parentNode){
            currentToast.parentNode.removeChild(currentToast);
          }
          currentToast = null;
        }, 260);
      }
    }, dur);
  };

  window.toast = window.dynamicIslandToast;

  /* ===== 3. SwiftUI Picker 下拉选择器系统 ===== */
  var valueDesc = Object.getOwnPropertyDescriptor(HTMLSelectElement.prototype, 'value');
  var openWrap = null;

  function isSkipped(select){
    return select.multiple || select.size > 1 || !!select.querySelector('optgroup') || select.hasAttribute('data-no-swift-picker');
  }
  function isHiddenSelect(select){
    return select.hidden || select.style.display === 'none';
  }
  function signature(select){
    var out = '';
    for(var i = 0; i < select.options.length; i++){
      var option = select.options[i];
      out += option.value + '|' + option.textContent + '|' + (option.hidden ? 1 : 0) + '|' + (option.disabled ? 1 : 0) + ';';
    }
    return out + (select.disabled ? 1 : 0);
  }

  function closeAll(){
    if(!openWrap) return;
    openWrap.classList.remove('swift-picker-open');
    if(openWrap.mui && openWrap.mui.menu) openWrap.mui.menu.hidden = true;
    openWrap = null;
  }

  function position(wrap){
    var menu = wrap.mui.menu, rect = wrap.getBoundingClientRect();
    menu.style.width = Math.max(rect.width, 120) + 'px';
    menu.hidden = false;
    var height = menu.offsetHeight, top = rect.bottom + 4;
    if(top + height > window.innerHeight - 8 && rect.top - 4 - height > 8) top = rect.top - 4 - height;
    menu.style.left = Math.max(8, Math.min(rect.left, window.innerWidth - rect.width - 8)) + 'px';
    menu.style.top = top + 'px';
  }

  function open(wrap){
    if(wrap.mui.select.disabled) return;
    if(openWrap === wrap){ closeAll(); return; }
    closeAll();
    rebuild(wrap);
    openWrap = wrap;
    wrap.classList.add('swift-picker-open');
    position(wrap);
    var active = wrap.mui.menu.querySelector('.mui-selected') || wrap.mui.menu.querySelector('li');
    if(active) wrap.mui.menu.scrollTop = Math.max(0, active.offsetTop - wrap.mui.menu.clientHeight / 2 + active.offsetHeight / 2);
  }

  function rebuild(wrap){
    var select = wrap.mui.select, menu = wrap.mui.menu, html = '';
    for(var i = 0; i < select.options.length; i++){
      var option = select.options[i];
      if(option.hidden) continue;
      html += '<li data-index="' + i + '" class="' + (option.selected ? 'mui-selected' : '') + (option.disabled ? ' mui-disabled' : '') + '" role="option">' + option.textContent.replace(/[&<>]/g, function(ch){ return ch === '&' ? '&amp;' : ch === '<' ? '&lt;' : '&gt;'; }) + '</li>';
    }
    menu.innerHTML = html || '<li class="mui-empty"></li>';
    wrap.mui.sig = signature(select);
  }

  function refresh(wrap){
    var select = wrap.mui.select;
    wrap.hidden = isHiddenSelect(select);
    wrap.classList.toggle('swift-picker-disabled', select.disabled);
    var option = select.options[select.selectedIndex];
    wrap.mui.value.textContent = option ? option.textContent : '';
    if(signature(select) !== wrap.mui.sig) rebuild(wrap);
    else {
      var items = wrap.mui.menu.children;
      for(var i = 0; i < items.length; i++){
        if(items[i].dataset.index === undefined) continue;
        items[i].classList.toggle('mui-selected', !!select.options[Number(items[i].dataset.index)]?.selected);
      }
    }
    if(openWrap === wrap) position(wrap);
  }

  function pick(wrap, index){
    var select = wrap.mui.select;
    valueDesc.set.call(select, select.options[index].value);
    closeAll();
    refresh(wrap);
    select.dispatchEvent(new Event('change', { bubbles: true }));
  }

  function upgrade(select){
    if(select.dataset.swiftPicker || isSkipped(select) || isHiddenSelect(select)) return;
    select.dataset.swiftPicker = '1';
    var wrap = document.createElement('div');
    wrap.className = ('swift-picker ' + (select.className || '')).trim();
    wrap.tabIndex = 0;
    wrap.setAttribute('role', 'combobox');
    if(select.style.width) wrap.style.width = select.style.width;
    select.parentNode.insertBefore(wrap, select);
    wrap.appendChild(select);
    select.classList.add('mui-native-select');
    var selection = document.createElement('span');
    selection.className = 'swift-picker-selection';
    var value = document.createElement('span');
    value.className = 'mui-select-value';
    selection.appendChild(value);
    selection.insertAdjacentHTML('beforeend', ARROW);
    wrap.appendChild(selection);
    var menu = document.createElement('ul');
    menu.className = 'swift-picker-menu';
    menu.hidden = true;
    document.body.appendChild(menu);
    wrap.mui = { select: select, menu: menu, value: value, sig: '' };

    Object.defineProperty(select, 'value', {
      configurable: true,
      get: function(){ return valueDesc.get.call(this); },
      set: function(next){ valueDesc.set.call(this, next); queueRefresh(select); }
    });

    wrap.addEventListener('click', function(event){ event.preventDefault(); open(wrap); });
    wrap.addEventListener('keydown', function(event){
      if(event.key === 'Enter' || event.key === ' '){ event.preventDefault(); open(wrap); return; }
      if(event.key === 'Escape'){ closeAll(); return; }
      if(event.key !== 'ArrowDown' && event.key !== 'ArrowUp') return;
      event.preventDefault();
      if(!wrap.classList.contains('swift-picker-open')){ open(wrap); return; }
      var items = [].slice.call(wrap.mui.menu.children).filter(function(item){ return !item.classList.contains('mui-empty'); });
      if(!items.length) return;
      var current = items.findIndex(function(item){ return item.classList.contains('mui-active'); });
      var next = event.key === 'ArrowDown' ? Math.min(items.length - 1, current + 1) : Math.max(0, current < 0 ? items.length - 1 : current - 1);
      items.forEach(function(item, index){ item.classList.toggle('mui-active', index === next); });
    });
    menu.addEventListener('mousedown', function(event){ event.preventDefault(); });
    menu.addEventListener('click', function(event){
      var item = event.target.closest('li');
      if(item && item.dataset.index !== undefined) pick(wrap, Number(item.dataset.index));
    });
    select.addEventListener('change', function(){ queueRefresh(select); });

    new MutationObserver(function(){ queueRefresh(select); }).observe(select, {
      childList: true,
      subtree: true,
      characterData: true,
      attributes: true,
      attributeFilter: ['hidden', 'disabled', 'class', 'style']
    });
    refresh(wrap);
  }

  var pending = new Set();
  function queueRefresh(select){
    if(pending.has(select)) return;
    pending.add(select);
    Promise.resolve().then(function(){
      pending.delete(select);
      var wrap = select.parentElement;
      if(wrap && wrap.mui && wrap.mui.select === select) refresh(wrap);
      else if(!select.dataset.swiftPicker) upgrade(select);
    });
  }

  function scan(root){
    (root || document).querySelectorAll('select').forEach(function(select){ upgrade(select); });
  }

  document.addEventListener('pointerdown', function(event){
    if(openWrap && !openWrap.contains(event.target) && !openWrap.mui.menu.contains(event.target)) closeAll();
  }, true);
  document.addEventListener('keydown', function(event){
    if(event.key === 'Escape') closeAll();
  });
  document.addEventListener('scroll', function(){
    if(openWrap) position(openWrap);
  }, true);
  window.addEventListener('resize', function(){
    if(openWrap) position(openWrap);
  });
  document.addEventListener('reset', function(event){
    var form = event.target;
    if(!(form instanceof HTMLFormElement)) return;
    Promise.resolve().then(function(){ form.querySelectorAll('select').forEach(queueRefresh); });
  }, true);

  new MutationObserver(function(records){
    for(var i = 0; i < records.length; i++){
      var node = records[i].target;
      if(records[i].type === 'attributes'){
        if(node.tagName === 'SELECT') queueRefresh(node);
        continue;
      }
      records[i].addedNodes.forEach(function(added){
        if(added.nodeType !== 1) return;
        if(added.tagName === 'SELECT') upgrade(added);
        else if(added.querySelectorAll) added.querySelectorAll('select').forEach(function(select){ upgrade(select); });
      });
    }
  }).observe(document.body, { childList: true, subtree: true, attributes: true, attributeFilter: ['hidden', 'style', 'class'] });

  /* ===== 4. SwiftUI Searchable Combobox 增强器 ===== */
  function upgradeCombobox(input, getOptions){
    if(!input || input.dataset.swiftCombobox) return input ? input.closest('.swift-combobox') : null;
    input.dataset.swiftCombobox = '1';
    input.classList.add('swift-combobox-input');
    input.autocomplete = 'off';

    var wrap = document.createElement('div');
    wrap.className = 'swift-combobox' + (input.disabled ? ' swift-picker-disabled' : '');
    wrap.tabIndex = -1;
    wrap.setAttribute('role', 'combobox');

    input.parentNode.insertBefore(wrap, input);
    var selection = document.createElement('div');
    selection.className = 'swift-picker-selection';
    selection.appendChild(input);
    selection.insertAdjacentHTML('beforeend', ARROW);
    wrap.appendChild(selection);

    var arrow = selection.querySelector('.mui-select-arrow');
    var menu = document.createElement('ul');
    menu.className = 'swift-picker-menu';
    menu.hidden = true;
    document.body.appendChild(menu);

    wrap.mui = {
      isCombobox: true,
      input: input,
      menu: menu,
      arrow: arrow,
      getOptions: getOptions || function(){ return []; },
      filterActive: false
    };

    function esc(s){
      return String(s || '').replace(/[&<>"]/g, function(ch){
        return ch === '&' ? '&amp;' : ch === '<' ? '&lt;' : ch === '>' ? '&gt;' : '&quot;';
      });
    }

    function rebuild(){
      var raw = typeof wrap.mui.getOptions === 'function' ? wrap.mui.getOptions() : (wrap.mui.getOptions || []);
      var items = [];
      for(var i = 0; i < raw.length; i++){
        var opt = raw[i];
        if(typeof opt === 'string'){
          items.push({ value: opt, label: opt, sublabel: '' });
        } else if(opt && typeof opt === 'object'){
          var val = opt.value !== undefined ? String(opt.value) : (opt.code || opt.id || opt.label || '');
          var lbl = opt.label !== undefined ? String(opt.label) : (opt.name || val);
          var sub = opt.sublabel || opt.sub || (opt.code && opt.label ? opt.label : '');
          items.push({ value: val, label: lbl, sublabel: sub });
        }
      }

      var current = (input.value || '').trim();
      var filter = (wrap.mui.filterActive ? current : '').toLowerCase();

      var list = items;
      if(filter){
        list = items.filter(function(it){
          return it.value.toLowerCase().indexOf(filter) !== -1 ||
                 it.label.toLowerCase().indexOf(filter) !== -1 ||
                 (it.sublabel && it.sublabel.toLowerCase().indexOf(filter) !== -1);
        });
      }

      if(!list.length){
        menu.innerHTML = '<li class="mui-empty" style="color:var(--muted-2);cursor:default;font-style:italic">None</li>';
        return;
      }

      var html = '';
      for(var j = 0; j < list.length; j++){
        var item = list[j];
        var isSelected = current && (item.value.toLowerCase() === current.toLowerCase() || item.label.toLowerCase() === current.toLowerCase());
        html += '<li data-value="' + esc(item.value) + '" class="' + (isSelected ? 'mui-selected' : '') + ' mui-combobox-item" role="option">';
        if(item.sublabel && item.sublabel !== item.value){
          html += '<span class="mui-combobox-val">' + esc(item.value) + '</span><span class="mui-combobox-sub">' + esc(item.sublabel) + '</span>';
        } else {
          html += '<span class="mui-combobox-val">' + esc(item.label) + '</span>';
        }
        html += '</li>';
      }
      menu.innerHTML = html;
    }

    function open(resetFilter){
      if(input.disabled) return;
      if(openWrap === wrap && !resetFilter){
        closeAll();
        return;
      }
      closeAll();
      wrap.mui.filterActive = !resetFilter;
      rebuild();
      openWrap = wrap;
      wrap.classList.add('swift-picker-open');
      position(wrap);
      var active = menu.querySelector('.mui-selected') || menu.querySelector('li:not(.mui-empty)');
      if(active) menu.scrollTop = Math.max(0, active.offsetTop - menu.clientHeight / 2 + active.offsetHeight / 2);
    }

    function pick(value){
      input.value = value;
      closeAll();
      refresh();
      wrap.mui.picking = true;
      try {
        input.dispatchEvent(new Event('input', { bubbles: true }));
        input.dispatchEvent(new Event('change', { bubbles: true }));
      } finally {
        wrap.mui.picking = false;
      }
    }

    function refresh(){
      wrap.hidden = isHiddenSelect(input);
      wrap.classList.toggle('swift-picker-disabled', !!input.disabled);
      if(openWrap === wrap){
        if(input.disabled) closeAll();
        else position(wrap);
      }
    }

    input.addEventListener('focus', function(){
      if(!input.disabled && !wrap.classList.contains('swift-picker-open')){
        open(true);
      }
    });
    input.addEventListener('click', function(){
      if(!input.disabled && !wrap.classList.contains('swift-picker-open')){
        open(true);
      }
    });

    arrow.addEventListener('click', function(event){
      event.preventDefault();
      event.stopPropagation();
      if(input.disabled) return;
      if(wrap.classList.contains('swift-picker-open')){
        closeAll();
      } else {
        input.focus();
        open(true);
      }
    });

    input.addEventListener('input', function(){
      if(input.disabled || wrap.mui.picking) return;
      wrap.mui.filterActive = true;
      if(!wrap.classList.contains('swift-picker-open')){
        open(false);
      } else {
        rebuild();
        position(wrap);
      }
    });

    menu.addEventListener('mousedown', function(event){
      event.preventDefault();
    });
    menu.addEventListener('click', function(event){
      var item = event.target.closest('li');
      if(item && item.dataset.value !== undefined){
        pick(item.dataset.value);
      }
    });

    input.addEventListener('keydown', function(event){
      if(event.key === 'Escape'){
        if(wrap.classList.contains('swift-picker-open')){
          event.preventDefault();
          event.stopPropagation();
          closeAll();
        }
        return;
      }
      if(event.key === 'ArrowDown' || event.key === 'ArrowUp'){
        event.preventDefault();
        if(!wrap.classList.contains('swift-picker-open')){
          open(true);
          return;
        }
        var items = [].slice.call(menu.children).filter(function(it){ return !it.classList.contains('mui-empty'); });
        if(!items.length) return;
        var current = items.findIndex(function(it){ return it.classList.contains('mui-active'); });
        var next = event.key === 'ArrowDown' ? Math.min(items.length - 1, current + 1) : Math.max(0, current < 0 ? items.length - 1 : current - 1);
        items.forEach(function(it, idx){ it.classList.toggle('mui-active', idx === next); });
        if(items[next]){
          menu.scrollTop = Math.max(0, items[next].offsetTop - menu.clientHeight / 2 + items[next].offsetHeight / 2);
        }
        return;
      }
      if(event.key === 'Enter'){
        if(wrap.classList.contains('swift-picker-open')){
          var active = menu.querySelector('li.mui-active');
          if(active && active.dataset.value !== undefined){
            event.preventDefault();
            event.stopPropagation();
            pick(active.dataset.value);
          }
        }
      }
    });

    wrap.addEventListener('focusout', function(e){
      if(openWrap === wrap && (!e.relatedTarget || (!wrap.contains(e.relatedTarget) && !menu.contains(e.relatedTarget)))){
        closeAll();
      }
    });

    new MutationObserver(function(){
      refresh();
    }).observe(input, { attributes: true, attributeFilter: ['disabled', 'hidden', 'class', 'style'] });

    var inputDisabledDesc = Object.getOwnPropertyDescriptor(HTMLInputElement.prototype, 'disabled') || (typeof Element !== 'undefined' ? Object.getOwnPropertyDescriptor(Element.prototype, 'disabled') : null);
    if(inputDisabledDesc && inputDisabledDesc.set){
      Object.defineProperty(input, 'disabled', {
        configurable: true,
        get: function(){ return inputDisabledDesc.get.call(this); },
        set: function(val){
          inputDisabledDesc.set.call(this, val);
          refresh();
        }
      });
    }

    refresh();
    return wrap;
  }

  injectStyle();
  if(document.readyState === 'loading') document.addEventListener('DOMContentLoaded', function(){ scan(); });
  else scan();

  window.swiftPicker = {
    scan: scan,
    upgrade: upgrade,
    upgradeCombobox: upgradeCombobox,
    closeAll: closeAll
  };

  window.muiUpgradeSelects = scan;
  window.muiUpgradeCombobox = upgradeCombobox;
  window.muiCloseSelects = closeAll;
})();
