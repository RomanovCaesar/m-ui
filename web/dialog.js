/* 面板自己的确认框和输入框，替代浏览器自带的 confirm / prompt 对话框
 *
 * 浏览器的对话框是系统样式（Chrome 里是一个黑色小窗），不跟随面板主题和语言，
 * 还会挡住整个页面的脚本。这里用和面板其他弹窗一样的 Liquid Glass 窗口：
 *
 *   if (!await muiConfirm(muiT('msg.confirmDeleteClient'), {danger: true})) return;
 *   const name = await muiPrompt(muiT('subtpl.renamePrompt'), currentName);  // 取消时为 null
 *
 * danger 用红色按钮，并且默认焦点在「取消」上，误按回车不会执行删除。
 * Esc 和点击遮罩都等于取消。
 */
(() => {
  const queue = [];
  let active = null;

  const button = (className, text) => {
    const element = document.createElement('button');
    element.type = 'button';
    element.className = className;
    element.textContent = text;
    return element;
  };

  const show = ({title, message, input, confirmText, danger}) => new Promise(resolve => {
    const job = () => {
      const backdrop = document.createElement('div');
      backdrop.className = 'modal-backdrop mui-dialog open';
      const panel = document.createElement('section');
      panel.className = 'modal-panel narrow mui-dialog-panel';
      panel.setAttribute('role', input ? 'dialog' : 'alertdialog');
      panel.setAttribute('aria-modal', 'true');

      const header = document.createElement('header');
      header.className = 'modal-titlebar';
      header.textContent = title;
      header.id = 'mui-dialog-title-' + Date.now();
      panel.setAttribute('aria-labelledby', header.id);

      const content = document.createElement('div');
      content.className = 'modal-content mui-dialog-content';
      const text = document.createElement('p');
      text.className = 'mui-dialog-message';
      text.textContent = message;
      content.appendChild(text);
      let field = null;
      if (input) {
        field = document.createElement('input');
        field.className = 'mui-dialog-input';
        field.value = input.value ?? '';
        field.spellcheck = false;
        field.setAttribute('aria-label', message);
        content.appendChild(field);
      }

      const footer = document.createElement('footer');
      footer.className = 'modal-footer';
      const cancel = button('outline-btn', muiT('common.cancel'));
      const ok = button(danger ? 'danger-btn' : 'primary-btn', confirmText || muiT('common.confirm'));
      footer.append(cancel, ok);
      panel.append(header, content, footer);
      backdrop.appendChild(panel);

      const previousFocus = document.activeElement;
      const finish = value => {
        document.removeEventListener('keydown', onKey, true);
        backdrop.remove();
        active = null;
        if (previousFocus && document.contains(previousFocus)) previousFocus.focus?.({preventScroll: true});
        resolve(value);
        const next = queue.shift();
        if (next) next();
      };
      const accept = () => finish(field ? field.value : true);
      const dismiss = () => finish(field ? null : false);
      const onKey = event => {
        if (event.key === 'Escape') { event.preventDefault(); event.stopPropagation(); dismiss(); }
        else if (event.key === 'Enter' && !event.isComposing && (event.target === field || event.target === ok)) { event.preventDefault(); event.stopPropagation(); accept(); }
        else if (event.key === 'Tab') {
          // Keep the focus inside the dialog.
          const stops = [field, cancel, ok].filter(Boolean);
          const index = stops.indexOf(document.activeElement);
          event.preventDefault();
          stops[(index + (event.shiftKey ? stops.length - 1 : 1)) % stops.length].focus();
        }
      };
      ok.onclick = accept;
      cancel.onclick = dismiss;
      backdrop.addEventListener('mousedown', event => { if (event.target === backdrop) dismiss(); });
      document.addEventListener('keydown', onKey, true);
      document.body.appendChild(backdrop);
      active = backdrop;
      requestAnimationFrame(() => {
        if (field) { field.focus(); field.select(); }
        else (danger ? cancel : ok).focus();
      });
    };
    // One dialog at a time: a second request waits for the first answer.
    if (active) queue.push(job); else job();
  });

  window.muiConfirm = (message, options = {}) => show({
    title: options.title || muiT('dialog.confirmTitle'),
    message,
    confirmText: options.confirmText,
    danger: !!options.danger,
  });

  window.muiPrompt = (message, value = '', options = {}) => show({
    title: options.title || muiT('dialog.promptTitle'),
    message,
    input: {value},
    confirmText: options.confirmText,
    danger: false,
  });
})();
