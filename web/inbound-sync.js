(() => {
  const menu = $('#general-menu');
  if (!menu) return;
  let action = menu.querySelector('[data-general="sync-inbound"]');
  if (!action) {
    action = document.createElement('button');
    action.type = 'button'; action.dataset.general = 'sync-inbound';
    action.innerHTML = '<span class="general-icon">'+AI('sync')+'</span><span class="general-text" data-i18n="inbounds.sync">Sync Inbound</span>';
    const delBtn = menu.querySelector('[data-general="delete-depleted"]');
    if (delBtn) menu.insertBefore(action, delBtn);
    else menu.appendChild(action);
  }
  let data = null, selected = new Set(), peers = new Set(), lastJob = '', pollTimer = null, posting = false, pendingRequest = null, lastJobState = null;
  const createModal = (id, className, titleKey, content) => {
    const modal = document.createElement('div'); modal.id = id; modal.className = 'modal-backdrop';
    modal.innerHTML = `<section class="modal-panel ${className}" role="dialog" aria-modal="true" aria-labelledby="${id}-title"><header class="modal-titlebar"><span id="${id}-title" data-i18n="${titleKey}">${muiT(titleKey)}</span><button type="button" class="modal-close" data-si-close aria-label="关闭" data-i18n-aria="common.close">${AI('close')}</button></header>${content}</section>`;
    document.body.appendChild(modal); return modal;
  };
  const picker = createModal('sync-inbound-modal', 'si-picker', 'inbounds.sync', `<div class="si-layout"><aside class="si-panels"><h3 data-i18n="si.targetPanels">目标面板</h3><label class="si-panel-all" data-i18n-multi="si.allPanels"><input id="si-all-panels" type="checkbox">全选面板</label><div id="si-panels"></div></aside><div class="si-matrix-pane"><h3 data-i18n="si.matrixTitle">Username × Inbound</h3><div class="si-legend"><span data-i18n-multi="si.legendOn"><i class="si-swatch on"></i>已选择</span><span data-i18n-multi="si.legendOff"><i class="si-swatch"></i>未选择</span><span data-i18n-multi="si.legendNa"><i class="si-swatch na"></i>无对应 Client</span></div><div class="si-matrix-wrap" id="si-matrix"></div><p class="si-note" data-i18n="si.note1">点击 Username、Inbound 或单个格子选择。仅同步实际存在的 Client；SS、Snell 等单 Client 入站只有一个有效格子。重复同步合并选中的 Client，保留目标其余 Client 和流量统计。</p><p class="si-note" data-i18n="si.note2">将同步入站端口、配置及认证信息（包含所用证书与私钥）。目标有名称或端口冲突时会报错；保存后需在目标重启 Mihomo 生效。</p><div class="si-error" id="si-error" role="alert" hidden></div></div></div><footer class="modal-footer"><span class="si-footer-summary" id="si-summary"></span><button type="button" class="outline-btn" id="si-cancel" data-i18n="common.cancel">取消</button><button type="button" class="primary-btn" id="si-sync" disabled data-i18n="si.sync">同步</button></footer>`);
  const confirmModal = createModal('sync-inbound-confirm', 'si-confirm', 'si.confirmTitle', `<div class="modal-content"><p id="si-confirm-summary"></p><p class="si-note" data-i18n="si.confirmNote">确认后会将所选入站和 Client 同步到这些面板。目标已有的同来源副本会更新；同名或同端口的其他入站会报告冲突。</p></div><footer class="modal-footer"><button type="button" class="outline-btn" id="si-confirm-cancel" data-i18n="common.cancel">取消</button><button type="button" class="primary-btn" id="si-confirm-submit" data-i18n="si.confirmSync">确认同步</button></footer>`);
  const progress = createModal('sync-inbound-progress', 'si-progress', 'si.progressTitle', `<div class="modal-content"><p id="si-progress-summary" role="status" aria-live="polite"></p><progress id="si-progress-bar" value="0" max="1"></progress><div id="si-results"></div><div id="si-progress-error" class="si-error" role="alert" hidden></div></div><footer class="modal-footer"><button type="button" class="outline-btn" id="si-retry" hidden>重新查询</button><button type="button" class="primary-btn" id="si-progress-close" data-i18n="common.close">关闭</button></footer>`);
  const error = message => { $('#si-error').textContent = message; $('#si-error').hidden = !message; };
  const progressError = message => { $('#si-progress-error').textContent = message; $('#si-progress-error').hidden = !message; };
  const cellKey = (row, col) => `${row}:${col}`;
  const idsAt = (row, col) => data.inbounds[col].users[data.usernames[row]] || [];
  const validKeys = (row = null, col = null) => {
    const keys = [];
    data.usernames.forEach((_, r) => data.inbounds.forEach((_, c) => {
      if ((row === null || r === row) && (col === null || c === col) && idsAt(r,c).length) keys.push(cellKey(r,c));
    })); return keys;
  };
  const syncCheckbox = (box, keys, set) => {
    const count = keys.filter(key => set.has(key)).length;
    box.checked = !!keys.length && count === keys.length; box.indeterminate = count > 0 && count < keys.length; box.disabled = !keys.length;
  };
  const countSelection = () => {
    const columns = new Set(); let clients = 0;
    for (const key of selected) { const [r,c] = key.split(':').map(Number); columns.add(c); clients += idsAt(r,c).length; }
    return { inbounds: columns.size, clients };
  };
  const summary = () => { const n = countSelection(); return muiT('si.summary', {peers: peers.size, inbounds: n.inbounds, clients: n.clients}); };
  function updateSelection() {
    syncCheckbox($('#si-all-panels'), data.peers.map(peer => peer.id), peers);
    $$('[data-si-peer]', picker).forEach(box => { box.checked = peers.has(box.dataset.siPeer); });
    $$('[data-si-cell]', picker).forEach(button => { const on = selected.has(button.dataset.siCell); button.setAttribute('aria-pressed', String(on)); button.innerHTML = button.disabled ? '—' : on ? AI('check-circle') : ''; });
    $$('[data-si-row]', picker).forEach(box => syncCheckbox(box, validKeys(Number(box.dataset.siRow)), selected));
    $$('[data-si-col]', picker).forEach(box => syncCheckbox(box, validKeys(null, Number(box.dataset.siCol)), selected));
    const all = $('#si-all-cells'); if (all) syncCheckbox(all,validKeys(),selected);
    $('#si-summary').textContent = summary(); $('#si-sync').disabled = !peers.size || !selected.size;
  }
  function toggleKeys(keys) { const remove = keys.every(key => selected.has(key)); for (const key of keys) remove ? selected.delete(key) : selected.add(key); updateSelection(); }
  function renderPicker() {
    $('#si-panels').innerHTML = data.peers.length ? data.peers.map(peer => `<label class="si-panel-row"><input type="checkbox" data-si-peer="${esc(peer.id)}"><span>${esc(new URL(peer.endpoint).host)}<small>${esc(peer.name)}</small></span></label>`).join('') : `<p class="si-note">${muiT('si.noPeers')}</p>`;
    $('#si-matrix').innerHTML = data.usernames.length && data.inbounds.length ? `<table class="si-matrix"><thead><tr><th><label class="si-axis"><input id="si-all-cells" type="checkbox">${muiT('si.usernameAll')}</label></th>${data.inbounds.map((inbound,c) => `<th scope="col"><label class="si-axis"><input type="checkbox" data-si-col="${c}" aria-label="${esc(muiT('si.selectInbound',{name:inbound.name}))}"><span>${esc(inbound.name)}<small>${esc(inbound.type)} :${inbound.port}</small></span></label></th>`).join('')}</tr></thead><tbody>${data.usernames.map((username,r) => `<tr><th scope="row"><label class="si-axis"><input type="checkbox" data-si-row="${r}" aria-label="${esc(muiT('si.selectUser',{name:username}))}"><span>${esc(username)}</span></label></th>${data.inbounds.map((inbound,c) => { const valid = idsAt(r,c).length > 0; return `<td><button type="button" class="si-cell" data-si-cell="${cellKey(r,c)}" aria-label="${esc(username)} / ${esc(inbound.name)}" aria-pressed="false" ${valid ? '' : 'disabled'} title="${esc(muiT(valid ? 'si.toggleCell' : 'si.noClientCell'))}"></button></td>`; }).join('')}</tr>`).join('')}</tbody></table>` : `<div class="empty">${muiT('si.emptyMatrix')}</div>`;
    updateSelection();
  }
  action.onclick = async () => {
    menu.classList.remove('open'); action.disabled = true;
    try {
      const options = await api('/api/inbound-sync');
      if (options.activeJob) { showProgress(options.activeJob); return; }
      data = options; data.usernames = [...new Set(data.inbounds.flatMap(inbound => Object.keys(inbound.users)))].sort((a,b) => a.localeCompare(b));
      selected = new Set(validKeys()); peers = new Set(data.peers.map(peer => peer.id)); pendingRequest = null;
      error(''); renderPicker(); openModal(picker.id); $('#si-cancel').focus();
    } catch(e) { toast(e.message,true); } finally { action.disabled = false; }
  };
  $('#si-all-panels').onchange = event => { peers = new Set(event.target.checked ? data.peers.map(peer => peer.id) : []); updateSelection(); };
  $('#si-panels').onchange = event => { const id = event.target.dataset.siPeer; if (!id) return; event.target.checked ? peers.add(id) : peers.delete(id); updateSelection(); };
  $('#si-matrix').onclick = event => { const button = event.target.closest('[data-si-cell]'); if (button && !button.disabled) toggleKeys([button.dataset.siCell]); };
  $('#si-matrix').onchange = event => {
    const box = event.target;
    if (box.id === 'si-all-cells') toggleKeys(validKeys());
    else if (box.dataset.siRow !== undefined) toggleKeys(validKeys(Number(box.dataset.siRow)));
    else if (box.dataset.siCol !== undefined) toggleKeys(validKeys(null,Number(box.dataset.siCol)));
  };
  function closePicker() { closeModal(picker); action.focus(); }
  function cancelConfirm() { closeModal(confirmModal); picker.inert = false; $('#si-sync').focus(); }
  $('#si-cancel').onclick = closePicker; $('[data-si-close]',picker).onclick = closePicker;
  $('#si-sync').onclick = () => { $('#si-confirm-summary').textContent = muiT('si.confirmText', {summary: summary()}); picker.inert = true; openModal(confirmModal.id); $('#si-confirm-cancel').focus(); };
  $('#si-confirm-cancel').onclick = cancelConfirm; $('[data-si-close]',confirmModal).onclick = cancelConfirm;
  $('#si-confirm-submit').onclick = async () => {
    if (posting) return;
    const selections = data.inbounds.flatMap((inbound,c) => {
      const clientIds = data.usernames.flatMap((_,r) => selected.has(cellKey(r,c)) ? idsAt(r,c) : []);
      return clientIds.length ? [{inboundId:inbound.id,revision:inbound.revision,clientIds}] : [];
    });
    const bytes = crypto.getRandomValues(new Uint8Array(18));
    pendingRequest = {jobId:[...bytes].map(b => b.toString(16).padStart(2,'0')).join(''),peerIds:[...peers],selections};
    closeModal(confirmModal); picker.inert = false; closeModal(picker); openModal(progress.id);
    $('#si-progress-summary').textContent = muiT('si.submitting'); $('#si-results').innerHTML = ''; $('#si-progress-bar').value = 0;
    await submitJob();
  };
  async function submitJob() {
    if (posting || !pendingRequest) return; posting = true; $('#si-retry').hidden = true; progressError('');
    try { const job = await api('/api/inbound-sync',{method:'POST',body:JSON.stringify(pendingRequest)}); lastJob = job.id; pendingRequest = null; showProgress(job.id); }
    catch(e) {
      // A dropped response does not imply the server failed to start the task.
      // Retry the same job ID so a confirmation can never launch two transfers.
      progressError(e.message); $('#si-progress-summary').textContent = muiT('si.submitUnconfirmed');
      $('#si-retry').textContent = muiT('si.retrySubmit'); $('#si-retry').hidden = false;
    } finally { posting = false; }
  }
  function renderProgress(job) {
    lastJobState = job;
    const statuses = {pending:'si.statusPending',running:'si.statusRunning',success:'si.statusSuccess',partial:'si.statusPartial',failed:'si.statusFailed'};
    const successes = job.targets.filter(target => target.status === 'success').length;
    $('#si-progress-summary').textContent = job.done ? muiT('si.doneSummary', {success: successes, failed: job.targets.length-successes}) : muiT('si.runningSummary', {completed: job.completed, total: job.targets.length});
    $('#si-progress-bar').max = job.targets.length || 1; $('#si-progress-bar').value = job.completed;
    $('#si-results').innerHTML = job.targets.map(target => `<section class="si-target"><header><strong>${esc(new URL(target.endpoint).host)}</strong><span class="si-status-${esc(target.status)}">${esc(statuses[target.status] ? muiT(statuses[target.status]) : muiT('si.statusUnknown'))}</span></header>${target.message ? `<p class="si-error">${esc(target.message)}</p>` : ''}${target.results.length ? `<ul>${target.results.map(result => `<li class="${result.status === 'failed' ? 'si-status-failed' : ''}">${esc(result.name)} · ${esc(muiT(result.status === 'created' ? 'si.resCreated' : result.status === 'updated' ? 'si.resUpdated' : 'si.resFailed'))} · ${result.clients} Client — ${esc(result.message)}</li>`).join('')}</ul>` : ''}</section>`).join('');
  }
  async function pollJob() {
    clearTimeout(pollTimer); if (!lastJob || !progress.classList.contains('open')) return;
    try {
      const job = await api('/api/inbound-sync?job='+encodeURIComponent(lastJob)); renderProgress(job); progressError(''); $('#si-retry').hidden = true;
      if (!job.done) pollTimer = setTimeout(pollJob,700);
    } catch(e) { progressError(e.message+muiT('si.pollErrorSuffix')); $('#si-retry').textContent = muiT('si.requery'); $('#si-retry').hidden = false; }
  }
  function showProgress(id) { lastJob = id; openModal(progress.id); $('#si-progress-summary').textContent = muiT('si.querying'); pollJob(); }
  function closeProgress() { clearTimeout(pollTimer); closeModal(progress); }
  $('#si-retry').onclick = () => pendingRequest ? submitJob() : pollJob();
  $('#si-progress-close').onclick = closeProgress; $('[data-si-close]',progress).onclick = closeProgress;
  document.addEventListener('keydown', event => {
    if (event.key !== 'Escape') return;
    if (confirmModal.classList.contains('open')) cancelConfirm();
    else if (progress.classList.contains('open')) closeProgress();
    else if (picker.classList.contains('open')) closePicker();
  });
  window.addEventListener('mui-langchange', () => {
    if (data && picker.classList.contains('open')) renderPicker();
    if (data && confirmModal.classList.contains('open')) $('#si-confirm-summary').textContent = muiT('si.confirmText', {summary: summary()});
    if (lastJobState) renderProgress(lastJobState);
  });
  window.addEventListener('pagehide',() => clearTimeout(pollTimer));
})();
