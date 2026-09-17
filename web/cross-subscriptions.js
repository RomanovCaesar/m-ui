(() => {
  const menu = $('#general-menu');
  if (!menu) return;
  let action = menu.querySelector('[data-general="cross-subscriptions"]');
  if (!action) {
    action = document.createElement('button'); action.type = 'button'; action.dataset.general = 'cross-subscriptions';
    action.innerHTML = '<span class="general-icon">'+SF('retweet')+'</span><span class="general-text" data-i18n="cs.menu">Export All Subscriptions (Cross Panel)</span>';
    const local = menu.querySelector('[data-general="subscriptions"]');
    if (local) local.after(action); else { const del = menu.querySelector('[data-general="delete-depleted"]'); del ? menu.insertBefore(action, del) : menu.appendChild(action); }
  }
  let modal = null, jobID = '', pollTimer = null, opening = false, lastView = null, lastJobState = null;
  const makeModal = () => {
    if (modal) return modal;
    modal = document.createElement('div'); modal.id = 'cross-subscriptions-modal'; modal.className = 'modal-backdrop';
    modal.innerHTML = `<section class="modal-panel cs-modal" role="dialog" aria-modal="true" aria-labelledby="cs-title"><header class="modal-titlebar"><span id="cs-title" data-i18n="cs.menu">Export All Subscriptions (Cross Panel)</span><button type="button" class="modal-close" id="cs-close" aria-label="关闭" data-i18n-aria="common.close">${SF('close')}</button></header><div class="modal-content"><div class="cs-actions"><button type="button" class="primary-btn" id="cs-pull" data-i18n="cs.pull">拉取全体 Inbound 信息</button><button type="button" class="outline-btn" id="cs-clear" data-i18n="cs.clearCache">清除远端缓存</button><span class="cs-spacer"></span><span class="cs-status" id="cs-cache-summary"></span></div><div class="cs-progress" id="cs-progress" hidden><strong id="cs-progress-title">正在拉取</strong><progress id="cs-progress-bar" value="0" max="1"></progress><span class="cs-status" id="cs-progress-status"></span><div id="cs-progress-targets"></div></div><div class="cs-url-section"><h3 data-i18n="cs.urlSection">跨面板订阅地址</h3><p data-i18n-html="cs.urlNote">裸地址打开订阅面板；在订阅面板中可复制各客户端地址。Clash 地址在同一地址后追加 <code>/clash</code>。</p><textarea class="cs-url-box" id="cs-urls" readonly placeholder="先点击“拉取全体 Inbound 信息”" data-i18n-placeholder="cs.urlPlaceholder"></textarea><div class="cs-actions"><button type="button" class="outline-btn" id="cs-download" data-i18n="cs.download">下载地址</button><button type="button" class="primary-btn" id="cs-copy" data-i18n="cs.copyUrls">复制地址</button></div></div><div class="cs-cache"><h3 data-i18n="cs.cacheSection">远端缓存</h3><div class="cs-cache-list" id="cs-cache-list"></div></div><div class="cs-error" id="cs-error" role="alert" hidden></div></div><footer class="modal-footer"><button type="button" class="outline-btn" id="cs-close-footer" data-i18n="common.close">关闭</button></footer></section>`;
    document.body.appendChild(modal);
    $('#cs-close').onclick = close; $('#cs-close-footer').onclick = close;
    modal.onclick = event => { if (event.target === modal) close(); };
    $('#cs-pull').onclick = pull; $('#cs-clear').onclick = clearCache;
    $('#cs-copy').onclick = () => copyShareText($('#cs-urls').value, muiT('cs.urlsCopied'));
    $('#cs-download').onclick = () => downloadText('m-ui-cross-panel-subscriptions.txt', $('#cs-urls').value ? $('#cs-urls').value + '\n' : '');
    return modal;
  };
  const setError = message => { $('#cs-error').textContent = message; $('#cs-error').hidden = !message; };
  const formatDate = value => !value || value.startsWith('0001-') ? '—' : new Date(value).toLocaleString();
  const formatAge = value => { if (!value || value.startsWith('0001-')) return muiT('cs.never'); const age = Math.max(0, Date.now() - new Date(value).getTime()); if (age < 60000) return muiT('cs.justNow'); if (age < 3600000) return muiT('cs.minutesAgo', {n: Math.floor(age / 60000)}); return muiT('cs.hoursAgo', {n: Math.floor(age / 3600000)}); };
  const renderView = view => {
    lastView = view;
    const urls = (view.users || []).map(user => user.url).filter(Boolean);
    $('#cs-urls').value = urls.join('\n');
    $('#cs-cache-summary').textContent = muiT('cs.cacheSummary', {sources: (view.sources || []).length, urls: urls.length});
    const list = $('#cs-cache-list');
    list.innerHTML = (view.sources || []).length ? view.sources.map(source => `<div class="cs-cache-row"><div>${esc(source.node.name || source.node.id)}<small>${esc(source.node.endpoint || muiT('mc.noEndpoint'))} · ${formatAge(source.retrievedAt)}（${esc(formatDate(source.retrievedAt))}）</small></div><span>${source.inboundCount} Inbound / ${source.clientCount} Client</span><span class="${source.lastError ? 'failed' : 'success'}">${source.lastError ? esc(source.lastError) : esc(muiT('cs.cached'))}</span></div>`).join('') : `<div class="cs-empty">${muiT('cs.emptyCache')}</div>`;
    if (view.activeJob && !jobID) { jobID = view.activeJob; showProgress(); }
  };
  const renderJob = job => {
    lastJobState = job;
    const title = $('#cs-progress-title'), status = $('#cs-progress-status'), bar = $('#cs-progress-bar'), targets = $('#cs-progress-targets');
    const done = job.done, success = job.targets.filter(target => target.status === 'success').length;
    title.textContent = muiT(done ? 'cs.pullDone' : 'cs.pullRunning'); status.textContent = done ? muiT('cs.jobDone', {success: success, failed: job.targets.length - success}) : muiT('cs.jobProgress', {completed: job.completed, total: job.targets.length});
    status.className = `cs-status ${done && success === job.targets.length ? 'success' : done ? 'failed' : ''}`;
    bar.max = job.targets.length || 1; bar.value = job.completed;
    const labels = {pending:'cs.statusPending',running:'cs.statusRunning',success:'cs.statusSuccess',failed:'cs.statusFailed'};
    targets.innerHTML = job.targets.map(target => `<div class="cs-cache-row"><div>${esc(target.endpoint || target.id)}</div><span>${target.inbounds || 0} Inbound / ${target.clients || 0} Client</span><span class="${target.status === 'success' ? 'success' : target.status === 'failed' ? 'failed' : ''}">${labels[target.status] ? esc(muiT(labels[target.status])) : esc(target.status)}${target.message ? `：${esc(target.message)}` : ''}</span></div>`).join('');
  };
  const showProgress = () => { $('#cs-progress').hidden = false; poll(); };
  const poll = async () => {
    clearTimeout(pollTimer); if (!jobID || !modal?.classList.contains('open')) return;
    try { const job = await api('/api/cross-subscriptions?job=' + encodeURIComponent(jobID)); renderJob(job); if (!job.done) pollTimer = setTimeout(poll, 700); else { jobID = ''; const view = await api('/api/cross-subscriptions'); renderView(view); } }
    catch (error) { setError(error.message + muiT('cs.pollErrorSuffix')); $('#cs-progress-status').textContent = muiT('cs.jobQueryFailed'); }
  };
  const open = async () => {
    makeModal(); opening = true; setError(''); openModal(modal.id); $('#cs-urls').value = ''; $('#cs-progress').hidden = true; $('#cs-pull').disabled = false; $('#cs-clear').disabled = false;
    try { const view = await api('/api/cross-subscriptions'); renderView(view); if (view.activeJob) { jobID = view.activeJob; showProgress(); } }
    catch (error) { setError(error.message); }
    finally { opening = false; }
  };
  const pull = async () => {
    if (opening || jobID) return;
    setError(''); $('#cs-pull').disabled = true;
    try { const view = await api('/api/cross-subscriptions', {method:'POST'}); jobID = view.activeJob || ''; $('#cs-progress').hidden = !jobID; if (jobID) poll(); else renderView(view); }
    catch (error) { setError(error.message); }
    finally { $('#cs-pull').disabled = false; }
  };
  const clearCache = async () => {
    if (jobID) { setError(muiT('cs.busyPulling')); return; }
    $('#cs-clear').disabled = true; setError('');
    try { const view = await api('/api/cross-subscriptions', {method:'DELETE'}); renderView(view); }
    catch (error) { setError(error.message); }
    finally { $('#cs-clear').disabled = false; }
  };
  const close = () => { clearTimeout(pollTimer); closeModal(modal); };
  action.onclick = open;
  document.addEventListener('keydown', event => { if (event.key === 'Escape' && modal?.classList.contains('open')) close(); });
  window.addEventListener('mui-langchange', () => {
    if (!modal) return;
    if (lastView) renderView(lastView);
    if (lastJobState) renderJob(lastJobState);
  });
  window.addEventListener('pagehide', () => clearTimeout(pollTimer));
})();
