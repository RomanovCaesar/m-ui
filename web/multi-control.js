(() => {
  const bar=$('#settings-tabbar'),auth=$('#settings-pane-authentication');
  if(!bar||!auth)return;
  let state=null,loading=false,busy=false,localDirty=false,timer=null,epoch=0;
  const tab=document.createElement('button');tab.type='button';tab.dataset.settingsTab='multi-control';tab.innerHTML='<svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true"><path d="M10.7 7.2 6.3 14.8M13.3 7.2l4.4 7.6M7.5 17h9"></path><circle cx="12" cy="5" r="2.5"></circle><circle cx="5" cy="17" r="2.5"></circle><circle cx="19" cy="17" r="2.5"></circle></svg><span>Multi-control</span>';
  bar.appendChild(tab);
  const pane=document.createElement('div');pane.className='settings-pane';pane.id='settings-pane-multi-control';
  pane.innerHTML=`<div class="mc-notice" data-i18n="mc.notice">所有面板都是对等节点，配对后自动发现网络成员并尝试直连。需要同步入站时，请在 Inbounds → General Actions → Sync Inbound 中选择目标和 Client。</div>
    <details class="setting-section" open><summary>Multi-control</summary><div class="setting-section-body">
      <div class="setting-row"><div><strong data-i18n="mc.pairingToken">Pairing Token</strong><p data-i18n="mc.tokenDesc">支持手动输入或生成 16-32 位小写字母和数字混合的 token，供其他面板加入你的对等网络。</p></div><div class="setting-control"><button type="button" class="outline-btn" id="mc-token-open" data-i18n="mc.manageToken">管理配对 Token</button></div></div>
      <div class="setting-row"><div><strong data-i18n="mc.peerConnections">Peer Connections</strong><p data-i18n="mc.peersDesc">输入任意成员的 IP、面板端口和 token 加入。新成员会继承网络中的成员信任。</p></div><div class="setting-control"><button type="button" class="primary-btn" id="mc-connections-open" data-i18n="mc.manageConnections">管理连接</button></div></div>
    </div></details>
    <details class="setting-section" open><summary data-i18n="mc.localNode">Local Node</summary><div class="setting-section-body"><form id="mc-local-form">
      <div class="setting-row"><div><label for="mc-name" data-i18n="mc.nodeName">Node Name</label></div><div class="setting-control"><input id="mc-name" maxlength="128" required></div></div>
      <div class="setting-row"><div><strong data-i18n="mc.nodeId">Node ID</strong><p data-i18n="mc.nodeIdDesc">本机独立生成的持久身份。</p></div><div class="mc-node-id" id="mc-id">—</div></div>
      <div class="setting-row"><div><label for="mc-endpoint" data-i18n="mc.advertisedAddress">Advertised Address</label><p data-i18n="mc.endpointDesc">其他服务器可直接访问的本面板地址，不包含 URI 路径；面板端口或公网地址变更后请在此更新。</p></div><div class="setting-control"><input id="mc-endpoint" placeholder="http://203.0.113.10:2053" required></div></div>
      <div class="mc-local-footer"><span id="mc-local-status">联机设置独立保存，立即生效，无需重启面板。</span><button class="primary-btn" type="submit" id="mc-local-save" data-i18n="mc.saveLocal">保存联机地址</button></div>
    </form></div></details>
    <div id="mc-summary" class="mc-summary"></div><div id="mc-error" role="alert" hidden></div>`;
  auth.parentNode.insertBefore(pane,auth);
  const tokenModal=document.createElement('div');tokenModal.className='modal-backdrop';tokenModal.id='mc-token-modal';
  tokenModal.innerHTML=`<section class="modal-panel mc-modal" role="dialog" aria-modal="true" aria-labelledby="mc-token-title"><header class="modal-titlebar"><span id="mc-token-title" data-i18n="mc.pairingToken">Pairing Token</span><button type="button" class="modal-close" data-mc-close aria-label="Close" data-i18n-aria="common.close">${AI('close')}</button></header><div class="modal-content"><p data-i18n="mc.tokenModalDesc">持有 token 的面板可与本机配对，并发现网络成员。支持手动输入或生成 16-32 位小写字母和数字混合格式；重新生成或停用后，已配对的节点仍保持联机。</p><div class="mc-token-box"><input id="mc-token" aria-label="Pairing Token" data-i18n-aria="mc.pairingToken" placeholder="输入 16-32 位小写字母和数字，或点击生成" data-i18n-placeholder="mc.tokenPlaceholder" minlength="16" maxlength="32" pattern="[a-z0-9]{16,32}" autocomplete="off" spellcheck="false"><button class="outline-btn" type="button" id="mc-token-copy" data-i18n="common.copy">复制</button></div><div class="mc-modal-actions"><button class="primary-btn" type="button" id="mc-token-save" data-i18n="mc.saveToken">保存 Token</button><button class="outline-btn" type="button" id="mc-token-generate">生成 Token</button><button class="danger-btn" type="button" id="mc-token-disable" data-i18n="mc.disableToken">停用 Token</button></div><div class="mc-modal-error" role="alert" hidden></div></div></section>`;
  const connectionsModal=document.createElement('div');connectionsModal.className='modal-backdrop';connectionsModal.id='mc-connections-modal';
  connectionsModal.innerHTML=`<section class="modal-panel mc-modal mc-wide" role="dialog" aria-modal="true" aria-labelledby="mc-connections-title"><header class="modal-titlebar"><span id="mc-connections-title" data-i18n="mc.peerConnections">Peer Connections</span><button type="button" class="modal-close" data-mc-close aria-label="Close" data-i18n-aria="common.close">${AI('close')}</button></header><div class="modal-content"><div class="mc-modal-actions"><button class="primary-btn" type="button" id="mc-add-toggle">${AI('plus')}<span data-i18n="mc.addConnection">添加连接</span></button><button class="outline-btn" type="button" id="mc-refresh">${AI('sync')}<span data-i18n="topbar.refresh">刷新状态</span></button></div><form id="mc-connect-form" hidden><div class="mc-connect-grid"><label data-i18n-multi="mc.ipHost">IP / Host<input name="host" required placeholder="203.0.113.20" autocomplete="off"></label><label data-i18n-multi="mc.panelPort">Panel Port<input name="port" required type="number" min="1" max="65535" value="2053"></label><label data-i18n-multi="mc.protocol">Protocol<select name="scheme"><option value="auto" data-i18n="mc.protoAuto">Auto (HTTPS / HTTP)</option><option value="http">HTTP</option><option value="https">HTTPS</option></select></label><label class="mc-connect-token">Token<input name="token" required minlength="16" maxlength="32" pattern="[a-z0-9]{16,32}" autocomplete="off" spellcheck="false" placeholder="16-32 位小写字母和数字混合" data-i18n-placeholder="mc.tokenHint"></label></div><button class="primary-btn" type="submit" id="mc-connect-submit" data-i18n="mc.connect">连接</button></form><p class="mc-note" data-i18n="mc.addressNote">地址必须能够从服务器之间直接访问。每个节点独立维护连接；暂时离线不会删除节点。HTTPS 使用正常证书校验，使用证书对应域名连接。</p><div id="mc-peer-list"></div><details id="mc-blocked-section" hidden><summary data-i18n="mc.blockedSection">本机已断开的节点</summary><div id="mc-blocked-list"></div></details><div class="mc-modal-error" role="alert" hidden></div></div></section>`;
  document.body.append(tokenModal,connectionsModal);
  function modalError(modal,message){const box=$('.mc-modal-error',modal);box.textContent=message;box.hidden=!message;}
  function error(message){$('#mc-error').textContent=message;$('#mc-error').hidden=!message;}
  function date(value){return !value||value.startsWith('0001-')?'—':new Date(value).toLocaleString();}
  function render(){
    if(!state)return;
    if(!localDirty){$('#mc-name').value=state.self.name;$('#mc-endpoint').value=state.self.endpoint||suggestEndpoint();}
    $('#mc-id').textContent=state.self.id;
    $('#mc-summary').textContent=muiT('mc.summary',{peers:state.peers.length,max:state.maxPeers,online:state.peers.filter(peer=>peer.online).length,token:muiT(state.tokenEnabled?'mc.tokenOn':'mc.tokenOff')});
    $('#mc-token-generate').textContent=muiT(state.tokenEnabled?'mc.regenerateToken':'mc.generateToken');$('#mc-token-disable').disabled=busy||!state.tokenEnabled;
    $('#mc-local-status').textContent=muiT(localDirty?'mc.localUnsaved':'mc.localHint');
    const list=$('#mc-peer-list');
    list.innerHTML=state.peers.length?`<div class="mc-table-wrap"><table class="mc-peer-table"><thead><tr><th>${muiT('mc.colNode')}</th><th>${muiT('mc.colAddress')}</th><th>${muiT('mc.colStatus')}</th><th>${muiT('mc.colLastSeen')}</th><th>${muiT('mc.colActions')}</th></tr></thead><tbody>${state.peers.map(peer=>`<tr><td>${esc(peer.name)}<small>${esc(peer.id)}</small></td><td>${esc(peer.endpoint||muiT('mc.noEndpoint'))}<small class="mc-peer-error" title="${esc(peer.error||'')}">${esc(peer.error||'')}</small></td><td><span class="mc-status ${peer.online?'online':''}">${peer.online?muiT('mc.online'):peer.lastSeen?.startsWith('0001-')?muiT('mc.connectingOffline'):muiT('mc.offline')}</span></td><td>${esc(date(peer.lastSeen))}</td><td><button type="button" class="outline-btn" data-mc-disconnect="${esc(peer.id)}">${muiT('mc.disconnect')}</button></td></tr>`).join('')}</tbody></table></div>`:`<div class="mc-empty">${muiT('mc.emptyPeers')}</div>`;
    $$('[data-mc-disconnect]',list).forEach(button=>button.onclick=()=>disconnect(button.dataset.mcDisconnect));
    $('#mc-blocked-section').hidden=!state.blocked.length;
    $('#mc-blocked-list').innerHTML=state.blocked.map(id=>`<div class="mc-blocked-row"><code>${esc(id)}</code><button class="outline-btn" type="button" data-mc-unblock="${esc(id)}">${muiT('mc.allowRediscover')}</button></div>`).join('');
    $$('[data-mc-unblock]').forEach(button=>button.onclick=()=>run(connectionsModal,async()=>{state=await api('/api/multi-control/unblock',{method:'POST',body:JSON.stringify({id:button.dataset.mcUnblock})});render();}));
  }
  function suggestEndpoint(){
    const settings=app.state?.state?.settings||{};
    const host=settings.panelDomain||location.hostname;
    return location.protocol+'//'+(host.includes(':')&&!host.startsWith('[')?'['+host+']':host)+':'+(location.port||(location.protocol==='https:'?443:80));
  }
  function setBusy(value){busy=value;if(value)epoch++;for(const button of $$('button',tokenModal).concat($$('button',connectionsModal),[$('#mc-local-save')]))button.disabled=value;if(!value)render();}
  async function run(modal,action){if(busy)return;setBusy(true);modal?modalError(modal,''):error('');try{await action();}catch(e){modal?modalError(modal,e.message):error(e.message);}finally{setBusy(false);}}
  async function load(){if(loading||busy)return;loading=true;const version=epoch;try{const result=await api('/api/multi-control');if(version===epoch){state=result;render();error('');}}catch(e){if(version===epoch)error(e.message);}finally{loading=false;}}
  async function saveLocal(){state=await api('/api/multi-control/settings',{method:'PUT',body:JSON.stringify({name:$('#mc-name').value.trim(),endpoint:$('#mc-endpoint').value.trim()})});localDirty=false;render();}
  tab.onclick=()=>{$$('[data-settings-tab]').forEach(item=>item.classList.toggle('active',item===tab));$$('.settings-pane').forEach(item=>item.classList.toggle('active',item===pane));if(typeof syncInkBars==='function')syncInkBars();load();};
  for(const field of [$('#mc-name'),$('#mc-endpoint')])field.oninput=()=>{localDirty=true;$('#mc-local-status').textContent=muiT('mc.localUnsaved');};
  $('#mc-local-form').onsubmit=event=>{event.preventDefault();run(null,saveLocal);};
  $('#mc-token-open').onclick=()=>{if(!state)return;openModal('mc-token-modal');run(tokenModal,async()=>{const data=await api('/api/multi-control/token');$('#mc-token').value=data.token;});};
  $('#mc-token-save').onclick=()=>run(tokenModal,async()=>{
    const token=$('#mc-token').value.trim().toLowerCase();
    if(!token)throw new Error(muiT('mc.tokenRequired'));
    if(!/^[a-z0-9]{16,32}$/.test(token)||!/[a-z]/.test(token)||!/[0-9]/.test(token)){
      throw new Error(muiT('mc.tokenFormat'));
    }
    await saveLocal();
    const data=await api('/api/multi-control/token',{method:'POST',body:JSON.stringify({token})});
    $('#mc-token').value=data.token;state.tokenEnabled=true;render();toast(muiT('mc.tokenSaved'));
  });
  $('#mc-token').onkeydown=event=>{if(event.key==='Enter'){event.preventDefault();$('#mc-token-save').click();}};
  $('#mc-token').oninput=()=>{modalError(tokenModal,'');};
  $('#mc-token-generate').onclick=()=>run(tokenModal,async()=>{await saveLocal();const data=await api('/api/multi-control/token',{method:'POST'});$('#mc-token').value=data.token;state.tokenEnabled=true;render();});
  $('#mc-token-disable').onclick=()=>run(tokenModal,async()=>{state=await api('/api/multi-control/token',{method:'DELETE'});$('#mc-token').value='';render();});
  $('#mc-token-copy').onclick=()=>copyShareText($('#mc-token').value,muiT('mc.tokenCopied'));
  $('#mc-connections-open').onclick=()=>{openModal('mc-connections-modal');load();};
  $('#mc-add-toggle').onclick=()=>{$('#mc-connect-form').hidden=!$('#mc-connect-form').hidden;};
  $('#mc-refresh').onclick=()=>run(connectionsModal,async()=>{await api('/api/multi-control/sync',{method:'POST'});state=await api('/api/multi-control');render();});
  $('#mc-connect-form').onsubmit=event=>{event.preventDefault();const form=event.currentTarget;run(connectionsModal,async()=>{await saveLocal();const data=Object.fromEntries(new FormData(form));data.port=Number(data.port);state=await api('/api/multi-control/connect',{method:'POST',body:JSON.stringify(data)});form.elements.token.value='';form.hidden=true;render();});};
  function disconnect(id){if(!confirm(muiT('mc.confirmDisconnect')))return;run(connectionsModal,async()=>{state=await api('/api/multi-control/disconnect',{method:'POST',body:JSON.stringify({id})});render();});}
  for(const modal of [tokenModal,connectionsModal]){
    $('[data-mc-close]',modal).onclick=()=>{if(!busy)closeModal(modal);};modal.onclick=event=>{if(event.target===modal&&!busy)closeModal(modal);};
  }
  document.addEventListener('keydown',event=>{if(event.key==='Escape'&&!busy){closeModal(tokenModal);closeModal(connectionsModal);}});
  window.addEventListener('mui-langchange',()=>{if(state)render();});
  timer=setInterval(()=>{if(app.currentView==='settings'&&(pane.classList.contains('active')||connectionsModal.classList.contains('open')))load();},5000);
  window.addEventListener('pagehide',()=>clearInterval(timer));
})();
