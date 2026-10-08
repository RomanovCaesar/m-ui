/* 订阅模版页（Subscription Templates）
 *
 * 列出内置模版（中国 / 俄国 / 伊朗三个国别变体）和用户导入的模版，选中哪个，
 * 订阅就按哪个生成各客户端的完整配置。导入时服务端会清掉节点、proxy-provider
 * 和注释，这里先预览清理结果再保存。
 *
 * 接口失败时 message 是 subTemplates.err.* 的 i18n 键，data.detail 是原始原因。
 */
(() => {
  const root = $('#subtpl-root');
  if (!root) return;
  let view = null, loading = false, viewModal = null, importModal = null;

  const request = async (path, options = {}) => {
    const response = await fetch(panelURL(path), {headers: {'Content-Type': 'application/json'}, ...options});
    let body = {};
    try { body = await response.json(); } catch {}
    if (response.status === 401) { location.href = panelURL('login'); throw new Error(muiT('msg.sessionExpired')); }
    if (!response.ok) {
      const key = body.message || '';
      const message = key.startsWith('subTemplates.err.') ? muiT(key) : (key || muiT('msg.requestFailed'));
      const detail = body.data && body.data.detail;
      throw new Error(detail ? `${message}: ${detail}` : message);
    }
    return body.data ?? body;
  };

  const variantLabel = variant => muiT('subtpl.variant.' + variant);
  const formatDate = value => value ? new Date(value).toLocaleString() : '';

  /* 内置模版那一行只建一次：变体选择器是液态玻璃标签栏，重建会丢掉它的动画状态。 */
  const builtinRow = document.createElement('div');
  builtinRow.className = 'setting-row subtpl-row';
  builtinRow.innerHTML = `<div class="subtpl-info"><strong><span data-i18n="subtpl.builtinName">Default template</span><span class="subtpl-badge" data-i18n="subtpl.inUse" hidden>In use</span></strong><p class="subtpl-meta" id="subtpl-builtin-meta"></p><p data-i18n="subtpl.builtinDesc">Same groups and rules for every country; only which country's traffic goes direct differs.</p></div><div class="setting-control subtpl-actions"><div class="inbound-status-tabs subtpl-variants" id="subtpl-variants" role="tablist"></div><button type="button" class="outline-btn" data-subtpl-view="builtin" data-i18n="subtpl.view">View</button><button type="button" class="primary-btn" data-subtpl-use="builtin" data-i18n="subtpl.use">Use</button></div>`;
  const customList = document.createElement('div');
  customList.id = 'subtpl-custom-list';
  $('#subtpl-list').append(builtinRow, customList);

  const variantBar = $('#subtpl-variants');
  const renderVariants = () => {
    /* 液态玻璃标签栏会先往容器里插两块 canvas，所以按按钮判断，追加而不是覆盖；
       当前变体在插入时就带 active，打开页面时滑块直接停在原位，不从第一项滑过去。 */
    if (variantBar.querySelector('[data-subtpl-variant]')) return;
    variantBar.insertAdjacentHTML('beforeend', (view?.variants || ['cn', 'ru', 'ir']).map(variant => `<button type="button" role="tab" data-subtpl-variant="${esc(variant)}"${variant === view?.variant ? ' class="active"' : ''}>${esc(variantLabel(variant))}</button>`).join(''));
    $$('[data-subtpl-variant]', variantBar).forEach(button => button.onclick = () => chooseVariant(button.dataset.subtplVariant));
  };

  const render = () => {
    if (!view) return;
    renderVariants();
    $$('[data-subtpl-variant]', variantBar).forEach(button => {
      const active = button.dataset.subtplVariant === view.variant;
      button.classList.toggle('active', active);
      button.setAttribute('aria-selected', String(active));
      button.textContent = variantLabel(button.dataset.subtplVariant);
    });
    const builtin = view.templates.find(item => item.builtin) || {};
    const builtinActive = view.active === 'builtin';
    builtinRow.classList.toggle('active', builtinActive);
    builtinRow.querySelector('.subtpl-badge').hidden = !builtinActive;
    builtinRow.querySelector('[data-subtpl-use]').hidden = builtinActive;
    $('#subtpl-builtin-meta').textContent = muiT('subtpl.meta', {groups: builtin.groups || 0, rules: builtin.rules || 0}) + ' · ' + variantLabel(view.variant);

    const custom = view.templates.filter(item => !item.builtin);
    customList.innerHTML = custom.length ? custom.map(item => {
      const active = view.active === item.id;
      return `<div class="setting-row subtpl-row${active ? ' active' : ''}"><div class="subtpl-info"><strong><span>${esc(item.name)}</span>${active ? `<span class="subtpl-badge">${esc(muiT('subtpl.inUse'))}</span>` : ''}</strong><p class="subtpl-meta">${esc(muiT('subtpl.meta', {groups: item.groups, rules: item.rules}))} · ${esc(muiT('subtpl.importedAt', {time: formatDate(item.createdAt)}))}</p></div><div class="setting-control subtpl-actions"><button type="button" class="outline-btn" data-subtpl-view="${esc(item.id)}">${esc(muiT('subtpl.view'))}</button><button type="button" class="outline-btn" data-subtpl-rename="${esc(item.id)}">${esc(muiT('subtpl.rename'))}</button><button type="button" class="danger-btn" data-subtpl-delete="${esc(item.id)}">${esc(muiT('common.delete'))}</button>${active ? '' : `<button type="button" class="primary-btn" data-subtpl-use="${esc(item.id)}">${esc(muiT('subtpl.use'))}</button>`}</div></div>`;
    }).join('') : `<div class="subtpl-empty">${esc(muiT('subtpl.empty'))}</div>`;
    $$('[data-subtpl-use]', root).forEach(button => button.onclick = () => activate(button.dataset.subtplUse));
    $$('[data-subtpl-view]', root).forEach(button => button.onclick = () => openView(button.dataset.subtplView));
    $$('[data-subtpl-rename]', root).forEach(button => button.onclick = () => rename(button.dataset.subtplRename));
    $$('[data-subtpl-delete]', root).forEach(button => button.onclick = () => remove(button.dataset.subtplDelete));
  };

  const load = async () => {
    if (loading) return;
    loading = true;
    try { view = await request('/api/sub-templates'); render(); }
    catch (error) { toast(error.message, true); }
    finally { loading = false; }
  };

  const activate = async id => {
    try { view = await request('/api/sub-templates/active', {method: 'POST', body: JSON.stringify({id})}); render(); toast(muiT('subtpl.activated')); }
    catch (error) { toast(error.message, true); }
  };

  /* 选变体就是在用内置模版：选中即切过去，省得再点一次“使用”。 */
  const chooseVariant = async variant => {
    if (!view || (view.variant === variant && view.active === 'builtin')) return;
    try { view = await request('/api/sub-templates/active', {method: 'POST', body: JSON.stringify({id: 'builtin', variant})}); render(); toast(muiT('subtpl.variantChanged', {variant: variantLabel(variant)})); }
    catch (error) { toast(error.message, true); render(); }
  };

  const findName = id => (view?.templates || []).find(item => item.id === id)?.name || '';

  const rename = async id => {
    const name = prompt(muiT('subtpl.renamePrompt'), findName(id));
    if (name === null || !name.trim()) return;
    try { view = await request('/api/sub-templates/' + encodeURIComponent(id), {method: 'PUT', body: JSON.stringify({name: name.trim()})}); render(); }
    catch (error) { toast(error.message, true); }
  };

  const remove = async id => {
    if (!confirm(muiT('subtpl.confirmDelete', {name: findName(id)}))) return;
    try { view = await request('/api/sub-templates/' + encodeURIComponent(id), {method: 'DELETE'}); render(); toast(muiT('subtpl.deleted')); }
    catch (error) { toast(error.message, true); }
  };

  /* ---- 查看 ---- */
  const makeViewModal = () => {
    if (viewModal) return viewModal;
    viewModal = document.createElement('div');
    viewModal.id = 'subtpl-view-modal';
    viewModal.className = 'modal-backdrop';
    viewModal.innerHTML = `<section class="modal-panel subtpl-modal" role="dialog" aria-modal="true" aria-labelledby="subtpl-view-title"><header class="modal-titlebar"><span id="subtpl-view-title"></span><button type="button" class="modal-close" data-subtpl-close aria-label="Close" data-i18n-aria="common.close">${SF('close')}</button></header><div class="modal-content"><textarea class="subtpl-code" id="subtpl-view-code" readonly spellcheck="false"></textarea></div><footer class="modal-footer"><button type="button" class="outline-btn" id="subtpl-view-download" data-i18n="subtpl.download">Download</button><button type="button" class="outline-btn" id="subtpl-view-copy" data-i18n="common.copy">Copy</button><button type="button" class="primary-btn" data-subtpl-close data-i18n="common.close">Close</button></footer></section>`;
    document.body.appendChild(viewModal);
    $$('[data-subtpl-close]', viewModal).forEach(button => button.onclick = () => closeModal(viewModal));
    viewModal.onclick = event => { if (event.target === viewModal) closeModal(viewModal); };
    $('#subtpl-view-copy').onclick = () => copyShareText($('#subtpl-view-code').value, muiT('subtpl.copied'));
    return viewModal;
  };

  const openView = async id => {
    makeViewModal();
    const builtin = id === 'builtin';
    const title = builtin ? `${muiT('subtpl.builtinName')} · ${variantLabel(view.variant)}` : findName(id);
    try {
      const data = await request('/api/sub-templates/content?id=' + encodeURIComponent(id) + (builtin ? '&variant=' + encodeURIComponent(view.variant) : ''));
      openModal(viewModal.id);
      $('#subtpl-view-title').textContent = title;
      $('#subtpl-view-code').value = data.content;
      $('#subtpl-view-download').onclick = () => downloadText(`m-ui-template-${builtin ? view.variant : id}.yaml`, data.content, 'application/yaml');
    } catch (error) { toast(error.message, true); }
  };

  /* ---- 导入 ---- */
  const makeImportModal = () => {
    if (importModal) return importModal;
    importModal = document.createElement('div');
    importModal.id = 'subtpl-import-modal';
    importModal.className = 'modal-backdrop';
    importModal.innerHTML = `<section class="modal-panel subtpl-modal" role="dialog" aria-modal="true" aria-labelledby="subtpl-import-title"><header class="modal-titlebar"><span id="subtpl-import-title" data-i18n="subtpl.importTitle">Import Template</span><button type="button" class="modal-close" data-subtpl-close aria-label="Close" data-i18n-aria="common.close">${SF('close')}</button></header><div class="modal-content"><p class="subtpl-note" data-i18n="subtpl.importNote">Paste a Mihomo subscription or template, or load it from a text file.</p><label class="subtpl-field"><span data-i18n="subtpl.name">Name</span><input id="subtpl-import-name" maxlength="64" autocomplete="off"></label><div class="subtpl-file"><button type="button" class="outline-btn" id="subtpl-import-file-button" data-i18n="subtpl.fromFile">Load from file</button><span id="subtpl-import-file-name"></span><input type="file" id="subtpl-import-file" accept=".yaml,.yml,.txt,.conf,text/plain,application/yaml" hidden></div><textarea class="subtpl-code" id="subtpl-import-content" spellcheck="false" placeholder="mixed-port: 7890&#10;proxy-groups:&#10;  ...&#10;rules:&#10;  ..."></textarea><div class="subtpl-preview" id="subtpl-import-preview" hidden><strong data-i18n="subtpl.previewTitle">After cleaning</strong><p id="subtpl-import-stats"></p><textarea class="subtpl-code" id="subtpl-import-cleaned" readonly spellcheck="false"></textarea></div><div class="cs-error" id="subtpl-import-error" role="alert" hidden></div></div><footer class="modal-footer"><button type="button" class="outline-btn" data-subtpl-close data-i18n="common.cancel">Cancel</button><button type="button" class="outline-btn" id="subtpl-import-preview-button" data-i18n="subtpl.preview">Preview</button><button type="button" class="primary-btn" id="subtpl-import-save" data-i18n="common.save">Save</button></footer></section>`;
    document.body.appendChild(importModal);
    $$('[data-subtpl-close]', importModal).forEach(button => button.onclick = () => closeModal(importModal));
    importModal.onclick = event => { if (event.target === importModal) closeModal(importModal); };
    const fileInput = $('#subtpl-import-file');
    $('#subtpl-import-file-button').onclick = () => fileInput.click();
    fileInput.onchange = async () => {
      const file = fileInput.files[0];
      if (!file) return;
      if (file.size > 2 * 1024 * 1024) { setImportError(muiT('subtpl.err.tooLarge')); fileInput.value = ''; return; }
      $('#subtpl-import-content').value = await file.text();
      $('#subtpl-import-file-name').textContent = file.name;
      if (!$('#subtpl-import-name').value.trim()) $('#subtpl-import-name').value = file.name.replace(/\.(ya?ml|txt|conf)$/i, '');
      fileInput.value = '';
      resetPreview();
    };
    $('#subtpl-import-content').oninput = resetPreview;
    $('#subtpl-import-preview-button').onclick = preview;
    $('#subtpl-import-save').onclick = save;
    return importModal;
  };

  const setImportError = message => { const box = $('#subtpl-import-error'); box.textContent = message; box.hidden = !message; };
  const resetPreview = () => { $('#subtpl-import-preview').hidden = true; setImportError(''); };

  const statsText = stats => {
    const parts = [muiT('subtpl.statsRemoved', {proxies: stats.removedProxies, providers: stats.removedProviders})];
    parts.push(muiT('subtpl.statsGroups', {groups: stats.groups, nodeGroups: stats.nodeGroups, rules: stats.rules}));
    if (stats.retargetedRules) parts.push(muiT('subtpl.statsRetargeted', {n: stats.retargetedRules}));
    return parts.join(' ');
  };

  const preview = async () => {
    const content = $('#subtpl-import-content').value;
    setImportError('');
    if (!content.trim()) { setImportError(muiT('subtpl.err.empty')); return; }
    try {
      const data = await request('/api/sub-templates/import', {method: 'POST', body: JSON.stringify({content, preview: true})});
      $('#subtpl-import-stats').textContent = statsText(data.stats);
      $('#subtpl-import-cleaned').value = data.content;
      $('#subtpl-import-preview').hidden = false;
    } catch (error) { setImportError(error.message); }
  };

  const save = async () => {
    const content = $('#subtpl-import-content').value, name = $('#subtpl-import-name').value.trim();
    setImportError('');
    if (!name) { setImportError(muiT('subtpl.err.nameRequired')); return; }
    if (!content.trim()) { setImportError(muiT('subtpl.err.empty')); return; }
    const button = $('#subtpl-import-save');
    button.disabled = true;
    try {
      const data = await request('/api/sub-templates/import', {method: 'POST', body: JSON.stringify({name, content})});
      view = data.view; render();
      closeModal(importModal);
      toast(muiT('subtpl.imported', {name}));
    } catch (error) { setImportError(error.message); }
    finally { button.disabled = false; }
  };

  const openImport = () => {
    makeImportModal();
    $('#subtpl-import-name').value = '';
    $('#subtpl-import-content').value = '';
    $('#subtpl-import-file-name').textContent = '';
    resetPreview();
    openModal(importModal.id);
  };

  $('#subtpl-import').onclick = openImport;
  /* refreshLanguageUI 切换语言后会调 render，动态生成的行和变体名跟着换。 */
  window.MUISubTemplates = {load, render};
})();
