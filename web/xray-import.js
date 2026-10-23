/* 从 Xray / 3x-ui 导入（仪表盘 → 管理 → 导入）
 *
 * 接受 3x-ui 的 x-ui.db（保留限额、到期、备注、禁用状态和已用流量）或 Xray 的
 * config.json，也可以直接粘贴 JSON。服务端只做追加：端口冲突时保留面板已有的入站，
 * 重名自动改名。导入完显示统计和需要注意的地方，完整的转换日志可以下载。
 */
(() => {
  const button = $('#overview-import');
  if (!button) return;
  let modal = null, busy = false, lastLog = '';

  const render = () => {
    modal = document.createElement('div');
    modal.className = 'modal-backdrop';
    modal.id = 'xray-import-modal';
    modal.innerHTML = `<section class="modal-panel narrow ximport-panel" role="dialog" aria-modal="true" aria-labelledby="ximport-title">
      <header class="modal-titlebar"><span id="ximport-title" data-i18n="ximport.title"></span><button class="modal-close" type="button" data-close-modal data-i18n-aria="common.close" aria-label="Close">${SF('close')}</button></header>
      <div class="modal-content">
        <form id="ximport-form" class="ximport-form">
          <p class="ximport-intro" data-i18n="ximport.intro"></p>
          <div class="ximport-file">
            <button class="outline-btn" type="button" id="ximport-pick">${SF('upload')} <span data-i18n="ximport.chooseFile"></span></button>
            <span class="ximport-file-name" id="ximport-file-name" data-i18n="ximport.noFile"></span>
            <input id="ximport-file" type="file" accept=".db,.json,application/json,application/octet-stream" hidden>
          </div>
          <textarea class="share-url-list ximport-paste" id="ximport-content" rows="6" spellcheck="false" data-i18n-placeholder="ximport.pastePlaceholder"></textarea>
          <div class="ximport-actions"><button class="primary-btn" type="submit" id="ximport-start">${SF('import')} <span data-i18n="ximport.start"></span></button></div>
        </form>
        <div id="ximport-result" class="ximport-result" hidden></div>
      </div>
    </section>`;
    document.body.appendChild(modal);
    if (window.muiApply) window.muiApply(modal);
    const file = $('#ximport-file', modal), name = $('#ximport-file-name', modal);
    $('#ximport-pick', modal).onclick = () => file.click();
    file.onchange = () => {
      const chosen = file.files && file.files[0];
      name.textContent = chosen ? chosen.name : muiT('ximport.noFile');
      name.classList.toggle('chosen', !!chosen);
    };
    $('#ximport-form', modal).onsubmit = submit;
    modal.addEventListener('click', event => {
      if (busy) return;
      if (event.target === modal || event.target.closest('[data-close-modal]')) closeModal(modal);
    });
  };

  const reset = () => {
    $('#ximport-form', modal).hidden = false;
    $('#ximport-result', modal).hidden = true;
    $('#ximport-file', modal).value = '';
    $('#ximport-file-name', modal).textContent = muiT('ximport.noFile');
    $('#ximport-file-name', modal).classList.remove('chosen');
    $('#ximport-content', modal).value = '';
  };

  async function submit(event) {
    event.preventDefault();
    if (busy) return;
    const file = $('#ximport-file', modal).files[0];
    const content = $('#ximport-content', modal).value.trim();
    if (!file && !content) { toast(muiT('ximport.needInput'), true); return; }
    const body = new FormData();
    if (file) body.append('file', file); else body.append('content', content);
    const start = $('#ximport-start', modal);
    busy = true; start.disabled = true;
    start.lastElementChild.textContent = muiT('ximport.importing');
    try {
      const response = await fetch(panelURL('api/import/xray'), {method: 'POST', body});
      let payload = {};
      try { payload = await response.json(); } catch {}
      if (response.status === 401) { location.href = panelURL('login'); return; }
      if (payload.data && payload.data.log) showResult(payload.data, response.ok ? '' : payload.message);
      else if (!response.ok) throw new Error(payload.message || muiT('msg.requestFailed'));
      if (response.ok && typeof refresh === 'function') refresh().catch(() => {});
    } catch (error) {
      toast(error.message, true);
    } finally {
      busy = false; start.disabled = false;
      start.lastElementChild.textContent = muiT('ximport.start');
    }
  }

  const stat = (label, imported, skipped) => `<div class="ximport-stat"><strong>${esc(String(imported))}</strong><span>${esc(label)}</span>${skipped ? `<small>${esc(muiT('ximport.skipped', {n: skipped}))}</small>` : ''}</div>`;

  function showResult(result, failure) {
    lastLog = result.log || '';
    const highlights = result.highlights || [];
    const box = $('#ximport-result', modal);
    box.innerHTML = `
      ${failure ? `<div class="ximport-failure">${esc(failure)}</div>` : ''}
      <div class="ximport-stats">
        ${stat(muiT('ximport.inbounds'), result.inbounds, result.inboundsSkipped)}
        ${stat(muiT('ximport.clients'), result.clients, 0)}
        ${stat(muiT('ximport.outbounds'), result.outbounds + result.groups, result.outboundsSkipped)}
        ${stat(muiT('ximport.rules'), result.rules, result.rulesSkipped)}
      </div>
      <p class="ximport-section-title">${esc(result.warnings ? muiT('ximport.warnings', {n: result.warnings}) : muiT('ximport.noWarnings'))}</p>
      ${highlights.length ? `<ul class="ximport-warnings">${highlights.map(line => `<li>${esc(line)}</li>`).join('')}</ul>` : ''}
      ${failure ? '' : `<p class="ximport-hint">${esc(muiT('ximport.restartHint'))}</p>`}
      <div class="ximport-actions">
        <button class="outline-btn" type="button" id="ximport-download">${SF('download')} <span>${esc(muiT('ximport.downloadLog'))}</span></button>
        <button class="outline-btn" type="button" id="ximport-again">${SF('plus')} <span>${esc(muiT('ximport.again'))}</span></button>
        ${failure ? '' : `<button class="primary-btn" type="button" id="ximport-restart">${SF('reload')} <span>${esc(muiT('ximport.restart'))}</span></button>`}
      </div>`;
    $('#ximport-form', modal).hidden = true;
    box.hidden = false;
    $('#ximport-download', box).onclick = downloadLog;
    $('#ximport-again', box).onclick = reset;
    const restart = $('#ximport-restart', box);
    if (restart) restart.onclick = async () => {
      if (typeof coreAction === 'function') await coreAction('restart');
      closeModal(modal);
    };
  }

  function downloadLog() {
    const stamp = new Date().toISOString().replace(/[:T]/g, '-').slice(0, 19);
    const blob = new Blob([lastLog], {type: 'text/plain;charset=utf-8'});
    const link = document.createElement('a');
    link.href = URL.createObjectURL(blob);
    link.download = `m-ui-import-${stamp}.txt`;
    document.body.appendChild(link);
    link.click();
    link.remove();
    setTimeout(() => URL.revokeObjectURL(link.href), 1000);
  }

  button.onclick = () => {
    if (!modal) render();
    if (!busy) reset();
    modal.classList.add('open');
  };
  window.addEventListener('mui-langchange', () => { if (modal && window.muiApply) window.muiApply(modal); });
})();
