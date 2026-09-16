/* ===== Ant Select 增强器 =====
   3x-ui 的下拉是 Ant Design Vue 1.x 的 a-select：1rem 圆角选择框、body 级浮层菜单、
   .5rem 圆角菜单项、选中项加粗浅底。本文件把页面里的原生 <select> 升级成同一观感：
   原生 select 原样留在 DOM 里（表单数据源、name/value/change 语义全部不变），
   外面套一层 .ant-select 外壳，菜单挂到 body 上用 fixed 定位，避免被抽屉 / 弹窗裁切。
   度量取自 3x-ui-2.9.3/web/assets/ant-design-vue/antd.min.css 与 custom.min.css。 */
(function(){
  'use strict';

  var STYLE_ID='mui-ant-select-style';
  var ARROW='<svg class="anticon mui-select-arrow" viewBox="64 64 896 896" aria-hidden="true"><path d="M884 256h-75c-5.1 0-9.9 2.5-12.9 6.6L512 651 227.9 262.6c-3-4.1-7.8-6.6-12.9-6.6h-75c-6.5 0-10.3 7.4-6.5 12.7l352.6 486.1c12.8 17.6 39 17.6 51.7 0l352.6-486.1c3.9-5.3.1-12.7-6.4-12.7z" fill="currentColor"></path></svg>';
  var CSS=[
    /* 外壳只做几何：:where() 零优先级，页面自己的宽度 / 外边距类（如 .language-select）能覆盖 */
    ':where(.ant-select){position:relative;display:block;width:100%;min-width:0}',
    '.ant-select .mui-native-select{position:absolute;top:0;left:0;width:1px;height:1px;margin:0;padding:0;border:0;opacity:0;pointer-events:none;appearance:none;-webkit-appearance:none}',
    '.ant-select[hidden]{display:none}',
    /* .ant-select-selection：32px、4px 11px、1rem 圆角、1px #d9d9d9 */
    '.ant-select-selection{display:flex;align-items:center;height:var(--ctl,32px);padding:0 28px 0 11px;border:1px solid var(--stroke,#d9d9d9);border-radius:var(--radius,1rem);background:var(--select-bg,var(--surface,#fff));color:var(--text,rgba(0,0,0,.65));font-size:14px;cursor:pointer;transition:border-color .3s var(--ease,cubic-bezier(.645,.045,.355,1)),background-color .3s var(--ease,cubic-bezier(.645,.045,.355,1)),box-shadow .3s var(--ease,cubic-bezier(.645,.045,.355,1))}',
    '.mui-select-value{flex:1;min-width:0;overflow:hidden;line-height:30px;text-overflow:ellipsis;white-space:nowrap}',
    '.ant-select-selection:hover{border-color:var(--teal-3,#18947b);background-color:var(--select-hover,#e8f4f2)}',
    '.ant-select-open .ant-select-selection,.ant-select:focus-visible .ant-select-selection{border-color:var(--teal,#008771);box-shadow:0 0 0 2px var(--teal-soft,rgba(0,135,113,.2));outline:0}',
    '.ant-select-disabled .ant-select-selection{background:var(--surface-2,#f5f5f5);color:var(--muted,rgba(0,0,0,.45));cursor:not-allowed}',
    '.ant-select-disabled .ant-select-selection:hover{border-color:var(--stroke,#d9d9d9);background:var(--surface-2,#f5f5f5)}',
    /* 箭头：right 11px、12px、rgba(0,0,0,.25)，展开转 180° */
    '.mui-select-arrow{position:absolute;top:50%;right:11px;margin-top:-6px;width:12px;height:12px;color:var(--muted-2,rgba(0,0,0,.25));pointer-events:none;transition:transform .3s var(--ease,cubic-bezier(.645,.045,.355,1))}',
    '.ant-select-open .mui-select-arrow{transform:rotate(180deg)}',
    /* .ant-select-dropdown：1rem 圆角、.5rem 内边距、0 2px 8px 阴影、max-height 250 */
    '.mui-select-menu{position:fixed;z-index:1050;margin:0;padding:.5rem;list-style:none;max-height:250px;overflow:auto;border-radius:var(--radius,1rem);background:var(--select-bg,var(--surface,#fff));box-shadow:var(--shadow-pop,0 2px 8px rgba(0,0,0,.15));animation:mui-select-in .15s var(--ease,cubic-bezier(.645,.045,.355,1))}',
    '.mui-select-menu[hidden]{display:none}',
    '@keyframes mui-select-in{0%{opacity:0;transform:translateY(-6px)}100%{opacity:1;transform:translateY(0)}}',
    /* .ant-select-dropdown-menu-item：5px 12px、22px 行高、.5rem 圆角、选中 600 + 浅底 */
    '.mui-select-menu li{margin:2px 0;padding:5px 12px;border-radius:var(--radius-sm,.5rem);color:var(--select-item-color,var(--text,rgba(0,0,0,.65)));font-size:14px;line-height:22px;cursor:pointer;transition:background-color .3s;overflow:hidden;text-overflow:ellipsis;white-space:nowrap}',
    '.mui-select-menu li:hover,.mui-select-menu li.mui-active{background:var(--select-item-hover,#e8f4f2)}',
    '.mui-select-menu li.mui-selected{background:var(--select-item-selected,#fafafa);font-weight:600}',
    '.mui-select-menu li:empty::after{content:"None";font-weight:400;color:var(--muted-2,rgba(0,0,0,.25))}',
    /* Ant Select Combobox (可输入下拉选单) */
    ':where(.ant-select-combobox){position:relative;display:block;width:100%;min-width:0}',
    '.ant-select-combobox .ant-select-selection{display:flex;align-items:center;height:var(--ctl,32px);padding:0 28px 0 11px;border:1px solid var(--stroke,#d9d9d9);border-radius:var(--radius,1rem);background:var(--select-bg,var(--surface,#fff));color:var(--text,rgba(0,0,0,.65));font-size:14px;cursor:text;transition:border-color .3s var(--ease,cubic-bezier(.645,.045,.355,1)),background-color .3s var(--ease,cubic-bezier(.645,.045,.355,1)),box-shadow .3s var(--ease,cubic-bezier(.645,.045,.355,1))}',
    '.ant-select-combobox .ant-select-search__field{flex:1;width:100%!important;height:100%!important;min-width:0!important;padding:0!important;margin:0!important;border:0!important;border-radius:0!important;outline:0!important;box-shadow:none!important;background:transparent!important;color:inherit!important;font:inherit!important;font-size:14px!important;line-height:30px!important;cursor:text}',
    '.ant-select-combobox:not(.ant-select-disabled) .ant-select-selection:hover{border-color:var(--teal-3,#18947b);background-color:var(--select-hover,#e8f4f2)}',
    '.ant-select-combobox.ant-select-open .ant-select-selection,.ant-select-combobox:focus-within .ant-select-selection{border-color:var(--teal,#008771);box-shadow:0 0 0 2px var(--teal-soft,rgba(0,135,113,.2));outline:0}',
    '.ant-select-combobox.ant-select-disabled .ant-select-selection{background:var(--surface-2,#f5f5f5);color:var(--muted,rgba(0,0,0,.45));cursor:not-allowed;border-color:var(--stroke,#d9d9d9)}',
    '.ant-select-combobox.ant-select-disabled .ant-select-search__field{cursor:not-allowed;color:var(--muted,rgba(0,0,0,.45))}',
    '.ant-select-combobox .mui-select-arrow{position:absolute;top:50%;right:11px;margin-top:-6px;width:12px;height:12px;color:var(--muted-2,rgba(0,0,0,.25));cursor:pointer;pointer-events:auto;transition:transform .3s var(--ease,cubic-bezier(.645,.045,.355,1))}',
    '.ant-select-combobox.ant-select-open .mui-select-arrow{transform:rotate(180deg)}',
    '.ant-select-combobox.ant-select-disabled .mui-select-arrow{cursor:not-allowed;pointer-events:none}',
    '.mui-combobox-item{display:flex;align-items:center;justify-content:space-between;gap:8px}',
    '.mui-combobox-sub{margin-left:auto;color:var(--muted,rgba(0,0,0,.45));font-size:12px;font-weight:400}'
  ].join('\n');

  function injectStyle(){
    if(document.getElementById(STYLE_ID))return;
    var style=document.createElement('style');
    style.id=STYLE_ID;
    style.textContent=CSS;
    document.head.appendChild(style);
  }

  var valueDesc=Object.getOwnPropertyDescriptor(HTMLSelectElement.prototype,'value');
  var openWrap=null;

  function isSkipped(select){
    return select.multiple||select.size>1||!!select.querySelector('optgroup')||select.hasAttribute('data-no-ant-select');
  }
  function isHiddenSelect(select){
    return select.hidden||select.style.display==='none';
  }
  function signature(select){
    var out='';
    for(var i=0;i<select.options.length;i++){
      var option=select.options[i];
      out+=option.value+'|'+option.textContent+'|'+(option.hidden?1:0)+'|'+(option.disabled?1:0)+';';
    }
    return out+(select.disabled?1:0);
  }

  function closeAll(){
    if(!openWrap)return;
    openWrap.classList.remove('ant-select-open');
    if(openWrap.mui&&openWrap.mui.menu)openWrap.mui.menu.hidden=true;
    openWrap=null;
  }

  function position(wrap){
    var menu=wrap.mui.menu,rect=wrap.getBoundingClientRect();
    menu.style.width=Math.max(rect.width,120)+'px';
    menu.hidden=false;
    var height=menu.offsetHeight,top=rect.bottom+4;
    if(top+height>window.innerHeight-8&&rect.top-4-height>8)top=rect.top-4-height;
    menu.style.left=Math.max(8,Math.min(rect.left,window.innerWidth-rect.width-8))+'px';
    menu.style.top=top+'px';
  }

  function open(wrap){
    if(wrap.mui.select.disabled)return;
    if(openWrap===wrap){closeAll();return;}
    closeAll();
    rebuild(wrap);
    openWrap=wrap;
    wrap.classList.add('ant-select-open');
    position(wrap);
    var active=wrap.mui.menu.querySelector('.mui-selected')||wrap.mui.menu.querySelector('li');
    if(active)wrap.mui.menu.scrollTop=Math.max(0,active.offsetTop-wrap.mui.menu.clientHeight/2+active.offsetHeight/2);
  }

  function rebuild(wrap){
    var select=wrap.mui.select,menu=wrap.mui.menu,html='';
    for(var i=0;i<select.options.length;i++){
      var option=select.options[i];
      if(option.hidden)continue;
      html+='<li data-index="'+i+'" class="'+(option.selected?'mui-selected':'')+(option.disabled?' mui-disabled':'')+'" role="option">'+option.textContent.replace(/[&<>]/g,function(ch){return ch==='&'?'&amp;':ch==='<'?'&lt;':'&gt;';})+'</li>';
    }
    menu.innerHTML=html||'<li class="mui-empty"></li>';
    wrap.mui.sig=signature(select);
  }

  function refresh(wrap){
    var select=wrap.mui.select;
    wrap.hidden=isHiddenSelect(select);
    wrap.classList.toggle('ant-select-disabled',select.disabled);
    var option=select.options[select.selectedIndex];
    wrap.mui.value.textContent=option?option.textContent:'';
    if(signature(select)!==wrap.mui.sig)rebuild(wrap);
    else{
      var items=wrap.mui.menu.children;
      for(var i=0;i<items.length;i++){
        if(items[i].dataset.index===undefined)continue;
        items[i].classList.toggle('mui-selected',!!select.options[Number(items[i].dataset.index)]?.selected);
      }
    }
    if(openWrap===wrap)position(wrap);
  }

  function pick(wrap,index){
    var select=wrap.mui.select;
    valueDesc.set.call(select,select.options[index].value);
    closeAll();
    refresh(wrap);
    select.dispatchEvent(new Event('change',{bubbles:true}));
  }

  function upgrade(select){
    if(select.dataset.antSelect||isSkipped(select)||isHiddenSelect(select))return;
    select.dataset.antSelect='1';
    var wrap=document.createElement('div');
    wrap.className=('ant-select '+(select.className||'')).trim();
    wrap.tabIndex=0;
    wrap.setAttribute('role','combobox');
    if(select.style.width)wrap.style.width=select.style.width;
    select.parentNode.insertBefore(wrap,select);
    wrap.appendChild(select);
    select.classList.add('mui-native-select');
    var selection=document.createElement('span');
    selection.className='ant-select-selection';
    var value=document.createElement('span');
    value.className='mui-select-value';
    selection.appendChild(value);
    selection.insertAdjacentHTML('beforeend',ARROW);
    wrap.appendChild(selection);
    var menu=document.createElement('ul');
    menu.className='mui-select-menu';
    menu.hidden=true;
    document.body.appendChild(menu);
    wrap.mui={select:select,menu:menu,value:value,sig:''};

    /* 代码里 select.value=… 的赋值也要刷新外壳 */
    Object.defineProperty(select,'value',{
      configurable:true,
      get:function(){return valueDesc.get.call(this);},
      set:function(next){valueDesc.set.call(this,next);queueRefresh(select);}
    });

    wrap.addEventListener('click',function(event){event.preventDefault();open(wrap);});
    wrap.addEventListener('keydown',function(event){
      if(event.key==='Enter'||event.key===' '){event.preventDefault();open(wrap);return;}
      if(event.key==='Escape'){closeAll();return;}
      if(event.key!=='ArrowDown'&&event.key!=='ArrowUp')return;
      event.preventDefault();
      if(!wrap.classList.contains('ant-select-open')){open(wrap);return;}
      var items=[].slice.call(wrap.mui.menu.children).filter(function(item){return !item.classList.contains('mui-empty');});
      if(!items.length)return;
      var current=items.findIndex(function(item){return item.classList.contains('mui-active');});
      var next=event.key==='ArrowDown'?Math.min(items.length-1,current+1):Math.max(0,current<0?items.length-1:current-1);
      items.forEach(function(item,index){item.classList.toggle('mui-active',index===next);});
    });
    menu.addEventListener('mousedown',function(event){event.preventDefault();});
    menu.addEventListener('click',function(event){
      var item=event.target.closest('li');
      if(item&&item.dataset.index!==undefined)pick(wrap,Number(item.dataset.index));
    });
    select.addEventListener('change',function(){queueRefresh(select);});

    new MutationObserver(function(){queueRefresh(select);}).observe(select,{childList:true,subtree:true,characterData:true,attributes:true,attributeFilter:['hidden','disabled','class','style']});
    refresh(wrap);
  }

  var pending=new Set();
  function queueRefresh(select){
    if(pending.has(select))return;
    pending.add(select);
    Promise.resolve().then(function(){
      pending.delete(select);
      var wrap=select.parentElement;
      if(wrap&&wrap.mui&&wrap.mui.select===select)refresh(wrap);
      else if(!select.dataset.antSelect)upgrade(select);
    });
  }

  function scan(root){
    (root||document).querySelectorAll('select').forEach(function(select){upgrade(select);});
  }

  document.addEventListener('pointerdown',function(event){
    // 菜单挂在 body 上、不在 wrap 内；点菜单自身（含选项）不能触发外部关闭，否则 click 到达前菜单已消失
    if(openWrap&&!openWrap.contains(event.target)&&!openWrap.mui.menu.contains(event.target))closeAll();
  },true);
  document.addEventListener('keydown',function(event){
    if(event.key==='Escape')closeAll();
  });
  document.addEventListener('scroll',function(){
    if(openWrap)position(openWrap);
  },true);
  window.addEventListener('resize',function(){
    if(openWrap)position(openWrap);
  });
  /* form.reset() 不触发 change，重置后统一刷新外壳 */
  document.addEventListener('reset',function(event){
    var form=event.target;
    if(!(form instanceof HTMLFormElement))return;
    Promise.resolve().then(function(){form.querySelectorAll('select').forEach(queueRefresh);});
  },true);

  new MutationObserver(function(records){
    for(var i=0;i<records.length;i++){
      var node=records[i].target;
      if(records[i].type==='attributes'){
        if(node.tagName==='SELECT')queueRefresh(node);
        continue;
      }
      records[i].addedNodes.forEach(function(added){
        if(added.nodeType!==1)return;
        if(added.tagName==='SELECT')upgrade(added);
        else if(added.querySelectorAll)added.querySelectorAll('select').forEach(function(select){upgrade(select);});
      });
    }
  }).observe(document.body,{childList:true,subtree:true,attributes:true,attributeFilter:['hidden','style','class']});

  function upgradeCombobox(input, getOptions){
    if(!input || input.dataset.antCombobox) return input ? input.closest('.ant-select-combobox') : null;
    input.dataset.antCombobox = '1';
    input.classList.add('ant-select-search__field');
    input.autocomplete = 'off';

    var wrap = document.createElement('div');
    wrap.className = 'ant-select ant-select-combobox' + (input.disabled ? ' ant-select-disabled' : '');
    wrap.tabIndex = -1;
    wrap.setAttribute('role', 'combobox');

    input.parentNode.insertBefore(wrap, input);
    var selection = document.createElement('div');
    selection.className = 'ant-select-selection';
    selection.appendChild(input);
    selection.insertAdjacentHTML('beforeend', ARROW);
    wrap.appendChild(selection);

    var arrow = selection.querySelector('.mui-select-arrow');
    var menu = document.createElement('ul');
    menu.className = 'mui-select-menu';
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
      wrap.classList.add('ant-select-open');
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
      wrap.classList.toggle('ant-select-disabled', !!input.disabled);
      if(openWrap === wrap){
        if(input.disabled) closeAll();
        else position(wrap);
      }
    }

    input.addEventListener('focus', function(){
      if(!input.disabled && !wrap.classList.contains('ant-select-open')){
        open(true);
      }
    });
    input.addEventListener('click', function(){
      if(!input.disabled && !wrap.classList.contains('ant-select-open')){
        open(true);
      }
    });

    arrow.addEventListener('click', function(event){
      event.preventDefault();
      event.stopPropagation();
      if(input.disabled) return;
      if(wrap.classList.contains('ant-select-open')){
        closeAll();
      } else {
        input.focus();
        open(true);
      }
    });

    input.addEventListener('input', function(){
      if(input.disabled || wrap.mui.picking) return;
      wrap.mui.filterActive = true;
      if(!wrap.classList.contains('ant-select-open')){
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
        if(wrap.classList.contains('ant-select-open')){
          event.preventDefault();
          event.stopPropagation();
          closeAll();
        }
        return;
      }
      if(event.key === 'ArrowDown' || event.key === 'ArrowUp'){
        event.preventDefault();
        if(!wrap.classList.contains('ant-select-open')){
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
        if(wrap.classList.contains('ant-select-open')){
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
  if(document.readyState==='loading')document.addEventListener('DOMContentLoaded',function(){scan();});
  else scan();
  window.muiUpgradeSelects=scan;
  window.muiUpgradeCombobox=upgradeCombobox;
  window.muiCloseSelects=closeAll;
})();
