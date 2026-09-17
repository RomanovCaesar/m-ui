(() => {
  const proxyTypes=[['direct','Direct'],['ss','Shadowsocks'],['snell','Snell'],['socks5','SOCKS5'],['http','HTTP / HTTPS'],['vmess','VMess'],['vless','VLESS'],['trojan','Trojan'],['hysteria2','Hysteria2'],['tuic','TUIC'],['wireguard','WireGuard'],['openvpn','OpenVPN']];
  const groupTypes=[['select','Select'],['url-test','URL Test'],['fallback','Fallback'],['load-balance','Load Balance']];
  const ruleTypes=['DOMAIN','DOMAIN-SUFFIX','DOMAIN-KEYWORD','DOMAIN-WILDCARD','DOMAIN-REGEX','GEOSITE','IP-CIDR','IP-CIDR6','IP-SUFFIX','IP-ASN','GEOIP','SRC-GEOIP','SRC-IP-ASN','SRC-IP-CIDR','SRC-IP-SUFFIX','DST-PORT','SRC-PORT','IN-PORT','IN-TYPE','IN-USER','IN-NAME','REMATCH-NAME','PROCESS-PATH','PROCESS-PATH-WILDCARD','PROCESS-PATH-REGEX','PROCESS-NAME','PROCESS-NAME-WILDCARD','PROCESS-NAME-REGEX','UID','NETWORK','DSCP','MATCH'];
  const builtins=['DIRECT','REJECT','REJECT-DROP','PASS','PASS-RULE','COMPATIBLE','GLOBAL'];
  const groupBuiltins=['DIRECT','REJECT','REJECT-DROP','COMPATIBLE'];
  const defaultTestURL='https://www.gstatic.com/generate_204';
  const defaultBasics=()=>({mode:'rule',directIpVersion:'dual',ipv6:true,tcpConcurrent:false,unifiedDelay:false,outboundTestUrl:defaultTestURL,trafficSampleSeconds:1,trafficSaveSeconds:5,outboundUploadStatistics:false,outboundDownloadStatistics:false,logLevel:'info',logBufferSize:300,maskLogAddress:false,blockIps:[],blockDomains:[],ipv4Domains:[],warpDomains:[]});
  const delayResults=new Map();
  let warpData=null,warpBusy=false;
  const clone=value=>JSON.parse(JSON.stringify(value??null));
  const root=$('#mihomo-settings-root');
  if(!root)return;

  let draft={outbounds:[],routingRules:[],basics:defaultBasics()},dirty=false,currentTab='basics',editingOutbound='',editingRule='',outboundMode='form',outboundBusy=false,editingOriginal=null;
  let outboundFormChanged=false,outboundYAMLCached=false;

  function freshID(prefix){return prefix+'-'+crypto.randomUUID().replaceAll('-','').slice(0,12);}
  function ensureFinalMatch(){
    if(!draft.routingRules.some(rule=>String(rule.type).toUpperCase()==='MATCH'))draft.routingRules.push({id:freshID('rule'),type:'MATCH',payload:'',target:'DIRECT',noResolve:false});
  }
  function syncFromState(){
    if(dirty)return;
    const state=app.state?.state||{};
    draft={outbounds:clone(state.outbounds||[])||[],routingRules:clone(state.routingRules||[])||[],basics:{...defaultBasics(),...clone(state.mihomoBasics||{}),mode:state.settings?.mode||'rule',logLevel:state.settings?.logLevel||'info'}};
    fillBasicsForm();
    ensureFinalMatch();
    render();
  }
  function markDirty(){dirty=true;root.classList.add('mihomo-dirty');syncButtons();render();}
  function syncButtons(){
    $('#mihomo-save').disabled=!dirty;
    $('#mihomo-restart').disabled=dirty;
  }
  function setTab(tab){
    currentTab=tab;
    $$('.mihomo-tab',root).forEach(button=>button.classList.toggle('active',button.dataset.mihomoTab===tab));
    $$('.mihomo-pane',root).forEach(pane=>pane.classList.toggle('active',pane.dataset.mihomoPane===tab));
    if(typeof syncInkBars==='function')syncInkBars();
    render();
  }
  function render(){
    renderWarpRouting();
    if(currentTab==='basics')renderBasicsIndicators();else if(currentTab==='routing')renderRules();else renderOutbounds();
    syncButtons();
  }
  function outboundAddress(item){
    if(item.vpngate)return `vpn_vpn${item.vpngate.slot} · table ${100+Number(item.vpngate.slot)}`;
    if(item.kind==='group')return (item.proxies||[]).join(', ');
    return `${item.server||'—'}${item.port?':'+item.port:''}`;
  }
  function outboundRows(){
    const fixed=[
      {name:'DIRECT',kind:'builtin',type:'direct',address:muiT('mh.directConnection')},
      {name:'REJECT',kind:'builtin',type:'reject',address:muiT('mh.rejectConnection')},
      {name:'REJECT-DROP',kind:'builtin',type:'reject-drop',address:muiT('mh.silentDrop')},
    ];
    if(draft.basics.directIpVersion!=='dual')fixed.push({name:'MUI-DIRECT',kind:'builtin',type:'direct',address:'Basics / '+draft.basics.directIpVersion});
    if(draft.basics.ipv4Domains.length)fixed.push({name:'MUI-IPV4',kind:'builtin',type:'direct',address:'Basics / IPv4'});
    return fixed.concat(draft.outbounds.map(item=>({...item,address:outboundAddress(item)})));
  }
  function delayLabel(name){const value=delayResults.get(name);return value==='Testing'?muiT('mh.testing'):value||'';}
  function outboundSupportsDelay(item){return !['reject','reject-drop'].includes(String(item.type||'').toLowerCase());}
  function renderOutbounds(){
    const box=$('#mihomo-outbound-table');
    const rows=outboundRows(),showTraffic=draft.basics.outboundUploadStatistics||draft.basics.outboundDownloadStatistics;
    box.innerHTML=`<div class="mihomo-table-wrap"><table class="mihomo-table"><thead><tr><th>#</th><th>${muiT('mh.colName')}</th><th>${muiT('mh.colKind')}</th><th>${muiT('mh.colStrategy')}</th><th>${muiT('mh.colMembers')}</th><th>Dialer Proxy</th>${showTraffic?`<th>${muiT('mh.colUpDown')}</th>`:''}<th>${muiT('mh.colTestResult')}</th><th>${muiT('mh.colTest')}</th><th>${muiT('mh.colActions')}</th></tr></thead><tbody>${rows.map((item,index)=>{
      const custom=item.kind!=='builtin',position=draft.outbounds.findIndex(entry=>entry.id===item.id),kind=item.vpngate?muiT('mh.vpngate'):item.kind==='group'?muiT('mh.kindGroup'):item.kind==='proxy'?muiT('mh.kindProxy'):muiT('mh.kindBuiltin'),type=item.vpngate?'VPNGate':item.type;
      return `<tr><td>${index+1}</td><td class="mihomo-name">${esc(item.name)}</td><td><span class="mihomo-pill ${item.kind==='group'?'group':''}">${esc(kind)}</span></td><td>${esc(type)}</td><td><div class="mihomo-member-summary" title="${esc(item.address||'')}">${esc(item.address||'—')}</div>${vpnGateStatusHTML(item)}</td><td>${esc(item.dialerProxy||'—')}</td>${showTraffic?'<td>'+fmt(app.state?.state?.outboundTraffic?.[item.name]?.up||0)+' / '+fmt(app.state?.state?.outboundTraffic?.[item.name]?.down||0)+'</td>':''}<td><span class="mihomo-delay-result" title="${esc(delayLabel(item.name))}">${esc(delayLabel(item.name)||'—')}</span></td><td><button type="button" class="mihomo-icon" data-test-outbound="${esc(item.name)}" title="${muiT('mh.testOutbound')}" ${dirty||delayResults.get(item.name)==='Testing'||!outboundSupportsDelay(item)||(!custom&&item.name!=='DIRECT')?'disabled':''}>${SF('thunderbolt')}</button></td><td><div class="mihomo-actions">${custom?`<button class="mihomo-icon" data-outbound-up="${esc(item.id)}" title="${muiT('mh.moveUp')}" ${position===0?'disabled':''}>${SF('arrow-up')}</button><button class="mihomo-icon" data-outbound-down="${esc(item.id)}" title="${muiT('mh.moveDown')}" ${position===draft.outbounds.length-1?'disabled':''}>${SF('arrow-down')}</button><button class="mihomo-icon" data-outbound-edit="${esc(item.id)}" title="${muiT('action.edit')}">${SF('edit')}</button><button class="mihomo-icon danger" data-outbound-delete="${esc(item.id)}" title="${muiT('common.delete')}">${SF('delete')}</button>`:'—'}</div></td></tr>`;
    }).join('')}</tbody></table></div>`;
    bindOutboundTable();
    $$('[data-test-outbound]',root).forEach(button=>button.onclick=()=>testOutbound(button));
  }
  function bindOutboundTable(){
    $$('[data-outbound-edit]',root).forEach(button=>button.onclick=()=>openOutboundModal(button.dataset.outboundEdit));
    $$('[data-outbound-delete]',root).forEach(button=>button.onclick=()=>deleteOutbound(button.dataset.outboundDelete));
    $$('[data-outbound-up]',root).forEach(button=>button.onclick=()=>moveOutbound(button.dataset.outboundUp,-1));
    $$('[data-outbound-down]',root).forEach(button=>button.onclick=()=>moveOutbound(button.dataset.outboundDown,1));
  }
  function moveOutbound(id,delta){
    const index=draft.outbounds.findIndex(item=>item.id===id),next=index+delta;
    if(index<0||next<0||next>=draft.outbounds.length)return;
    [draft.outbounds[index],draft.outbounds[next]]=[draft.outbounds[next],draft.outbounds[index]];
    markDirty();
  }
  function outboundDependencies(name){
    const result=[];
    for(const item of draft.outbounds){
      if(item.kind==='group'&&(item.proxies||[]).includes(name))result.push(muiT('mh.depGroup',{name:item.name}));
      if(item.kind==='proxy'&&item.dialerProxy===name)result.push(`Dialer Proxy ${item.name}`);
    }
    for(const rule of draft.routingRules)if(rule.target===name)result.push(muiT('mh.depRule',{type:rule.type}));
    if(currentWarpOutbound()?.name===name&&(draft.basics.warpDomains||[]).length)result.push('Basics / WARP Routing');
    return result;
  }
  function deleteOutbound(id){
    const item=draft.outbounds.find(candidate=>candidate.id===id);if(!item)return;
    const dependencies=outboundDependencies(item.name);
    if(dependencies.length){toast(muiT('mh.cannotDelete',{deps:dependencies.join(muiT('common.listSep')),name:item.name}),true);return;}
    if(!confirm(muiT('mh.confirmDeleteOutbound',{name:item.name})))return;
    draft.outbounds=draft.outbounds.filter(candidate=>candidate.id!==id);markDirty();
  }

  function basicsRuleRows(){
    const b=draft.basics,rows=[];
    const domain=(value,target)=>{const colon=value.indexOf(':'),prefix=colon<0?'domain':value.slice(0,colon),payload=colon<0?value:value.slice(colon+1);return {type:({geosite:'GEOSITE',domain:'DOMAIN-SUFFIX',full:'DOMAIN',keyword:'DOMAIN-KEYWORD'})[prefix]||'DOMAIN-SUFFIX',payload,target};};
    for(const value of b.blockIps||[])rows.push({type:value==='private'||value.startsWith('geoip:')?'GEOIP':'IP-CIDR',payload:value==='private'?'LAN':value.replace(/^geoip:/,''),target:'REJECT'});
    for(const value of b.blockDomains||[])rows.push(domain(value,'REJECT'));
    for(const value of b.ipv4Domains||[])rows.push(domain(value,'MUI-IPV4'));
    const warp=currentWarpOutbound();for(const value of b.warpDomains||[])rows.push(domain(value,warp?.name||'warp'));
    return rows;
  }
  function renderRules(){
    const box=$('#mihomo-rule-table');
    ensureFinalMatch();
    const managed=basicsRuleRows();
    const managedHTML=managed.map((rule,index)=>'<tr><td>'+(index+1)+'</td><td><span class="mihomo-pill">'+esc(rule.type)+'</span></td><td>'+esc(rule.payload)+'</td><td>'+esc(rule.target)+'</td><td>—</td><td><span class="mihomo-pill group">Basics</span></td></tr>').join('');
    box.innerHTML=`<div class="mihomo-table-wrap"><table class="mihomo-table"><thead><tr><th>#</th><th>${muiT('mh.colRuleType')}</th><th>${muiT('mh.colPayload')}</th><th>${muiT('mh.colOutbound')}</th><th>${muiT('mh.colOptions')}</th><th>${muiT('mh.colActions')}</th></tr></thead><tbody>${managedHTML}${draft.routingRules.map((rule,index)=>`<tr><td>${managed.length+index+1}</td><td><span class="mihomo-pill">${esc(rule.type)}</span></td><td><div class="mihomo-rule-payload" title="${esc(rule.payload||'')}">${esc(rule.type==='MATCH'?muiT('mh.matchAll'):rule.payload||'—')}</div></td><td class="mihomo-name">${esc(rule.target)}</td><td>${rule.noResolve?'<span class="mihomo-pill group">no-resolve</span>':'—'}</td><td><div class="mihomo-actions"><button class="mihomo-icon" data-rule-up="${esc(rule.id)}" title="${muiT('mh.moveUp')}" ${index===0||rule.type==='MATCH'?'disabled':''}>${SF('arrow-up')}</button><button class="mihomo-icon" data-rule-down="${esc(rule.id)}" title="${muiT('mh.moveDown')}" ${index===draft.routingRules.length-1||draft.routingRules[index+1]?.type==='MATCH'?'disabled':''}>${SF('arrow-down')}</button><button class="mihomo-icon" data-rule-edit="${esc(rule.id)}" title="${muiT('action.edit')}">${SF('edit')}</button><button class="mihomo-icon danger" data-rule-delete="${esc(rule.id)}" title="${muiT('common.delete')}">${SF('delete')}</button></div></td></tr>`).join('')}</tbody></table></div>`;
    $$('[data-rule-edit]',root).forEach(button=>button.onclick=()=>openRuleModal(button.dataset.ruleEdit));
    $$('[data-rule-delete]',root).forEach(button=>button.onclick=()=>deleteRule(button.dataset.ruleDelete));
    $$('[data-rule-up]',root).forEach(button=>button.onclick=()=>moveRule(button.dataset.ruleUp,-1));
    $$('[data-rule-down]',root).forEach(button=>button.onclick=()=>moveRule(button.dataset.ruleDown,1));
  }
  function moveRule(id,delta){
    const index=draft.routingRules.findIndex(rule=>rule.id===id),next=index+delta;
    if(index<0||next<0||next>=draft.routingRules.length||draft.routingRules[index].type==='MATCH'||draft.routingRules[next].type==='MATCH')return;
    [draft.routingRules[index],draft.routingRules[next]]=[draft.routingRules[next],draft.routingRules[index]];markDirty();
  }
  function deleteRule(id){
    const rule=draft.routingRules.find(item=>item.id===id);if(!rule)return;
    if(rule.type==='MATCH'){toast(muiT('mh.matchUndeletable'),true);return;}
    if(!confirm(muiT('mh.confirmDeleteRule',{type:rule.type})))return;
    draft.routingRules=draft.routingRules.filter(item=>item.id!==id);markDirty();
  }

  function targetOptions(selected='',forGroup=false,exclude=''){
    const base=forGroup?groupBuiltins:builtins;
    const names=[...base,...draft.outbounds.map(item=>item.name).filter(name=>name!==exclude)];
    return [...new Set(names)].map(name=>`<option value="${esc(name)}" ${name===selected?'selected':''}>${esc(name)}</option>`).join('');
  }
  function ensureModals(){
    if($('#mihomo-outbound-modal'))return;
    const outboundModal=document.createElement('div');outboundModal.className='modal-backdrop';outboundModal.id='mihomo-outbound-modal';outboundModal.innerHTML=`<section class="modal-panel mihomo-modal" role="dialog" aria-modal="true" aria-labelledby="mihomo-outbound-title"><header class="modal-titlebar"><span id="mihomo-outbound-title">Add Outbound</span><button type="button" class="modal-close" data-mihomo-close>${SF('close')}</button></header><form id="mihomo-outbound-form" novalidate><div class="modal-content"><div class="mihomo-editor-tabs" role="tablist" aria-label="Outbound input"><button type="button" role="tab" id="outbound-form-tab" aria-controls="outbound-form-panel" aria-selected="true" data-outbound-mode="form">Form</button><button type="button" role="tab" id="outbound-yaml-tab" aria-controls="outbound-yaml-panel" aria-selected="false" data-outbound-mode="yaml">YAML</button><span class="ink-bar"></span></div><div id="outbound-form-panel" role="tabpanel" aria-labelledby="outbound-form-tab"><div class="mihomo-form-note" data-i18n-html="mh.formNote">Proxy Node 会写入 <code>proxies</code>；Proxy Group 会写入 <code>proxy-groups</code>。两者都可作为 Routing Rule 的目标。</div><div class="mihomo-form-grid">
      <input name="id" type="hidden"><label class="mihomo-field"><span>Kind</span><select name="kind"><option value="proxy">Proxy Node</option><option value="group">Proxy Group</option></select></label><label class="mihomo-field"><span>Name</span><input name="name" maxlength="128" required placeholder="Hong Kong / Proxy"></label>
      <label class="mihomo-field"><span>Type</span><select name="type"></select></label><label class="mihomo-field" data-kind="proxy" data-types="ss,snell,socks5,http,vmess,vless,trojan,hysteria2,tuic,wireguard,openvpn"><span>Server</span><input name="server" placeholder="example.com"></label><label class="mihomo-field" data-kind="proxy" data-types="ss,snell,socks5,http,vmess,vless,trojan,hysteria2,tuic,wireguard,openvpn"><span>Port</span><input name="port" type="number" min="1" max="65535" placeholder="443"></label>
      <label class="mihomo-field" data-kind="proxy" data-types="socks5,http,openvpn"><span>Username</span><input name="username" autocomplete="off"></label><label class="mihomo-field" data-kind="proxy" data-types="ss,socks5,http,trojan,hysteria2,tuic,openvpn"><span>Password</span><input name="password" autocomplete="new-password"></label><label class="mihomo-field" data-kind="proxy" data-types="snell"><span data-i18n="mihomo.snellPsk">PSK</span><input name="snellPsk" autocomplete="new-password"></label>
      <label class="mihomo-field" data-kind="proxy" data-types="vmess,vless,tuic"><span>UUID</span><div style="display:flex;gap:7px"><input name="uuid" autocomplete="off"><button class="outline-btn" id="mihomo-generate-uuid" type="button" data-i18n="mh.generate">Generate</button></div></label><label class="mihomo-field" data-kind="proxy" data-types="tuic"><span>Token (TUIC v4)</span><input name="token" autocomplete="new-password"><small data-i18n="mh.tuicTokenNote">填写 Token，或使用 UUID + Password。</small></label>
      <label class="mihomo-field" data-kind="proxy" data-types="ss"><span>Cipher</span><select name="cipher"><option>aes-128-gcm</option><option selected>aes-256-gcm</option><option>chacha20-ietf-poly1305</option><option>2022-blake3-aes-128-gcm</option><option>2022-blake3-aes-256-gcm</option><option>2022-blake3-chacha20-poly1305</option></select></label><label class="mihomo-field" data-kind="proxy" data-types="vmess"><span>Alter ID</span><input name="alterId" type="number" min="0" value="0"></label>
      <label class="mihomo-field" data-kind="proxy" data-types="vmess"><span>VMess Cipher</span><select name="vmessCipher"><option>auto</option><option>none</option><option>aes-128-gcm</option><option>chacha20-poly1305</option></select></label><label class="mihomo-field" data-kind="proxy" data-types="vless"><span>Flow</span><input name="flow" placeholder="xtls-rprx-vision"></label><label class="mihomo-field" data-kind="proxy" data-types="vless"><span>Encryption</span><input name="encryption" value="none"></label>
      <label class="mihomo-field" data-kind="proxy" data-types="vmess,vless,trojan"><span>Network</span><select name="network"><option value="tcp">TCP</option><option value="ws">WebSocket</option><option value="grpc">gRPC</option></select></label><label class="mihomo-field" data-kind="proxy" data-network="ws"><span>WebSocket Path</span><input name="wsPath" placeholder="/"></label><label class="mihomo-field" data-kind="proxy" data-network="ws"><span>WebSocket Host</span><input name="wsHost"></label><label class="mihomo-field" data-kind="proxy" data-network="grpc"><span>gRPC Service Name</span><input name="grpcServiceName"></label>
      <label class="mihomo-field" data-kind="proxy" data-types="socks5,http,vmess,vless"><span>TLS</span><select name="tls"><option value="false">Disabled</option><option value="true">Enabled</option></select></label><label class="mihomo-field" data-kind="proxy" data-types="socks5,http,vmess,vless,trojan,hysteria2,tuic"><span>SNI / Server Name</span><input name="sni"></label><label class="mihomo-field" data-kind="proxy" data-types="vmess,vless,trojan,hysteria2,tuic"><span>ALPN</span><input name="alpn" placeholder="h2,http/1.1"></label><label class="mihomo-field" data-kind="proxy" data-types="ss,vmess,vless,trojan"><span>Client Fingerprint</span><input name="clientFingerprint" placeholder="chrome"></label>
      <label class="mihomo-field" data-kind="proxy" data-types="hysteria2"><span>Upload Bandwidth</span><input name="up" placeholder="100 Mbps"></label><label class="mihomo-field" data-kind="proxy" data-types="hysteria2"><span>Download Bandwidth</span><input name="down" placeholder="200 Mbps"></label><label class="mihomo-field" data-kind="proxy" data-types="hysteria2"><span>Obfs</span><select name="obfs"><option value="">None</option><option value="salamander">Salamander</option><option value="gecko">Gecko</option></select></label><label class="mihomo-field" data-kind="proxy" data-types="hysteria2"><span>Obfs Password</span><input name="obfsPassword"></label>
      <label class="mihomo-field" data-kind="proxy" data-types="tuic"><span>Congestion Controller</span><select name="congestionController"><option value="" data-i18n="proto.defaultPlain">Default</option><option value="cubic">cubic</option><option value="new_reno">new_reno</option><option value="bbr">bbr</option></select></label><label class="mihomo-field" data-kind="proxy" data-types="tuic"><span>UDP Relay Mode</span><select name="udpRelayMode"><option value="" data-i18n="proto.defaultPlain">Default</option><option value="native">native</option><option value="quic">quic</option></select></label>
      <div class="mihomo-field full" data-kind="proxy" data-types="snell"><span>Snell</span><div class="mihomo-form-grid"><label class="mihomo-field"><span data-i18n="mihomo.snellVersion">Version</span><select name="snellVersion"><option value="1">v1</option><option value="2">v2</option><option value="3">v3</option><option value="4">v4</option><option value="5">v5</option></select></label><label class="mihomo-check"><input name="snellReuse" type="checkbox"><span data-i18n="mihomo.snellReuse">Connection Reuse (v4/v5)</span></label><label class="mihomo-field"><span data-i18n="mihomo.snellObfsMode">Obfs Mode</span><select name="snellObfsMode"><option value="">none</option><option value="http">http</option><option value="tls">tls</option></select></label><label class="mihomo-field"><span data-i18n="mihomo.snellObfsHost">Obfs Host</span><input name="snellObfsHost" placeholder="bing.com"></label></div></div>
      <label class="mihomo-field" data-kind="proxy"><span>IP Version</span><select name="ipVersion"><option value="" data-i18n="proto.defaultDual">Default (dual)</option><option value="dual">dual</option><option value="ipv4">ipv4</option><option value="ipv6">ipv6</option><option value="ipv4-prefer">ipv4-prefer</option><option value="ipv6-prefer">ipv6-prefer</option></select></label><label class="mihomo-field" data-kind="proxy"><span>Interface Name</span><input name="interfaceName" placeholder="tun0 / ppp0"></label><label class="mihomo-field" data-kind="proxy"><span>Routing Mark</span><input name="routingMark" type="number" min="0" placeholder="0"></label><label class="mihomo-field" data-kind="proxy"><span>Dialer Proxy</span><select name="dialerProxy"></select></label>
      <div class="mihomo-field full" data-kind="proxy" data-types="wireguard"><span>WireGuard</span><div class="mihomo-form-grid"><label class="mihomo-field"><span>Local IPv4 / CIDR</span><input name="wireGuardIp" placeholder="10.0.0.2/24"></label><label class="mihomo-field"><span>Local IPv6 / CIDR</span><input name="wireGuardIpv6" placeholder="fd00::2/64"></label><label class="mihomo-field"><span>Private Key</span><input name="wireGuardPrivateKey" autocomplete="off"></label><label class="mihomo-field"><span>Peer Public Key</span><input name="wireGuardPublicKey" autocomplete="off"></label><label class="mihomo-field"><span>Pre-shared Key</span><input name="wireGuardPreSharedKey" autocomplete="off"></label><label class="mihomo-field"><span>Reserved</span><input name="wireGuardReserved" pattern="[0-9]{1,3}( *, *[0-9]{1,3}){2}" placeholder="209,98,59"></label><label class="mihomo-field"><span>MTU</span><input name="wireGuardMtu" type="number" min="0" placeholder="1420"></label><label class="mihomo-field"><span>Persistent Keepalive</span><input name="wireGuardPersistentKeepalive" type="number" min="0"></label><label class="mihomo-field"><span>Workers</span><input name="wireGuardWorkers" type="number" min="0"></label><label class="mihomo-field"><span>DNS</span><input name="wireGuardDns" placeholder="1.1.1.1,8.8.8.8"></label><label class="mihomo-check"><input name="wireGuardRemoteDnsResolve" type="checkbox">Remote DNS Resolve</label><label class="mihomo-field"><span>Refresh Server IP (s)</span><input name="wireGuardRefreshServerIpInterval" type="number" min="0"></label></div></div>
      <div class="mihomo-field full" data-kind="proxy" data-types="openvpn"><span>OpenVPN</span><div class="mihomo-form-grid"><label class="mihomo-field"><span>Protocol</span><select name="openVpnProto"><option value="udp">udp</option><option value="tcp">tcp</option></select></label><label class="mihomo-field"><span>Device</span><select name="openVpnDev"><option value="tun">tun</option></select></label><label class="mihomo-field"><span>Cipher</span><select name="openVpnCipher"><option>AES-128-GCM</option><option>AES-192-GCM</option><option>AES-256-GCM</option><option>AES-128-CBC</option><option>AES-192-CBC</option><option>AES-256-CBC</option><option>CHACHA20-POLY1305</option></select></label><label class="mihomo-field"><span>Auth</span><select name="openVpnAuth"><option>SHA256</option><option>MD5</option><option>SHA1</option><option>SHA384</option><option>SHA512</option></select></label><label class="mihomo-field"><span>Data Ciphers</span><input name="openVpnDataCiphers" placeholder="AES-256-GCM,AES-128-GCM"></label><label class="mihomo-field"><span>Fallback Cipher</span><input name="openVpnDataCipherFallback"></label><label class="mihomo-field"><span>Comp LZO</span><select name="openVpnCompLzo"><option value="">none</option><option>yes</option><option>no</option><option>adaptive</option></select></label><label class="mihomo-field"><span>Key Direction</span><select name="openVpnKeyDirection"><option value="">bidirectional</option><option>0</option><option>1</option></select></label><label class="mihomo-field full"><span>CA PEM</span><textarea name="openVpnCa" rows="3"></textarea></label><label class="mihomo-field full"><span>Client Cert PEM</span><textarea name="openVpnCert" rows="3"></textarea></label><label class="mihomo-field full"><span>Client Key PEM</span><textarea name="openVpnKey" rows="3"></textarea></label><label class="mihomo-field full"><span>TLS Auth PEM</span><textarea name="openVpnTlsAuth" rows="3"></textarea></label><label class="mihomo-field full"><span>TLS Crypt PEM</span><textarea name="openVpnTlsCrypt" rows="3"></textarea></label><label class="mihomo-field full"><span>TLS Crypt v2 PEM</span><textarea name="openVpnTlsCryptV2" rows="3"></textarea></label><label class="mihomo-field"><span>Ping (s)</span><input name="openVpnPing" type="number" min="0"></label><label class="mihomo-field"><span>Ping Restart (s)</span><input name="openVpnPingRestart" type="number" min="0"></label><label class="mihomo-field"><span>Handshake Timeout (s)</span><input name="openVpnHandshakeTimeout" type="number" min="0"></label><label class="mihomo-field"><span>MTU</span><input name="openVpnMtu" type="number" min="0"></label><label class="mihomo-field"><span>DNS</span><input name="openVpnDns" placeholder="1.1.1.1,8.8.8.8"></label><label class="mihomo-check"><input name="openVpnRemoteDnsResolve" type="checkbox">Remote DNS Resolve</label></div></div>
      <div class="mihomo-field full" data-kind="proxy"><span>Common Options</span><div class="mihomo-checks"><label class="mihomo-check" data-types="ss,snell,socks5,vmess,vless,trojan,wireguard"><input name="udp" type="checkbox">UDP</label><label class="mihomo-check"><input name="tfo" type="checkbox">TCP Fast Open</label><label class="mihomo-check"><input name="mptcp" type="checkbox">MPTCP</label><label class="mihomo-check" data-types="socks5,http,vmess,vless,trojan,hysteria2,tuic"><input name="skipCertVerify" type="checkbox">Skip Certificate Verify</label></div></div>
      <label class="mihomo-field full" data-kind="group"><span>Members</span><select name="proxies" multiple></select><small data-i18n="mh.membersNote">按 Ctrl / Cmd 多选；成员可以是代理、内置策略或其他无循环依赖的策略组。</small></label><label class="mihomo-field" data-kind="group" data-group-health><span>Test URL</span><input name="testUrl" value="${defaultTestURL}"></label><label class="mihomo-field" data-kind="group" data-group-health><span>Interval (seconds)</span><input name="interval" type="number" min="0" value="300"></label><label class="mihomo-field" data-kind="group" data-group-health><span>Timeout (ms)</span><input name="timeout" type="number" min="0" placeholder="5000"></label><label class="mihomo-field" data-kind="group" data-group-type="url-test"><span>Tolerance (ms)</span><input name="tolerance" type="number" min="0"></label><label class="mihomo-field" data-kind="group" data-group-type="load-balance"><span>Strategy</span><select name="strategy"><option value="consistent-hashing">consistent-hashing</option><option value="round-robin">round-robin</option><option value="sticky-sessions">sticky-sessions</option></select></label><label class="mihomo-field" data-kind="group" data-group-type="select"><span>Default Selected</span><select name="defaultSelected"></select></label><div class="mihomo-field" data-kind="group"><span>Group Options</span><label class="mihomo-check"><input name="disableUdp" type="checkbox">Disable UDP</label></div>
    </div></div><div id="outbound-yaml-panel" role="tabpanel" aria-labelledby="outbound-yaml-tab" hidden>
      <div class="outbound-link-row"><label for="outbound-share-link">Link</label><input id="outbound-share-link" type="text" autocomplete="off" spellcheck="false" placeholder="vmess:// / vless:// / trojan:// / ss:// / hysteria2://"><button type="button" id="outbound-parse-link" title="Parse Link to YAML" aria-label="Parse Link to YAML" data-i18n-title="mh.parseLink" data-i18n-aria="mh.parseLink">${SF('edit')}</button></div>
      <div class="outbound-yaml-editor"><pre id="outbound-yaml-lines" aria-hidden="true">1</pre><textarea id="outbound-yaml" aria-label="Outbound YAML" spellcheck="false" autocomplete="off" autocapitalize="off" wrap="off"></textarea></div>
    </div><div id="outbound-import-error" role="alert" hidden></div></div><footer class="modal-footer"><button type="button" class="outline-btn" data-mihomo-close data-i18n="common.cancel">Cancel</button><button type="submit" class="primary-btn" id="outbound-apply" data-i18n="mh.applyToDraft">Apply to Draft</button></footer></form></section>`;
    document.body.appendChild(outboundModal);

    const ruleModal=document.createElement('div');ruleModal.className='modal-backdrop';ruleModal.id='mihomo-rule-modal';ruleModal.innerHTML=`<section class="modal-panel mihomo-modal" role="dialog" aria-modal="true" aria-labelledby="mihomo-rule-title"><header class="modal-titlebar"><span id="mihomo-rule-title">Add Routing Rule</span><button type="button" class="modal-close" data-mihomo-close>${SF('close')}</button></header><form id="mihomo-rule-form"><div class="modal-content"><div class="mihomo-form-note" data-i18n="mh.ruleNote">Mihomo 从上到下匹配规则；首个命中的规则决定 Outbound。MATCH 固定作为最后兜底。</div><div class="mihomo-form-grid"><input name="id" type="hidden"><label class="mihomo-field"><span>Rule Type</span><select name="type">${ruleTypes.map(type=>`<option value="${type}">${type}</option>`).join('')}</select></label><label class="mihomo-field"><span>Outbound</span><select name="target"></select></label><label class="mihomo-field full" id="mihomo-rule-payload-field"><span>Payload</span><input name="payload" required><small id="mihomo-rule-hint"></small></label><div class="mihomo-field full" id="mihomo-no-resolve-field"><label class="mihomo-check" data-i18n-multi="mh.noResolveLabel"><input name="noResolve" type="checkbox">no-resolve（匹配目标 IP 时不触发 DNS 解析）</label></div></div></div><footer class="modal-footer"><button type="button" class="outline-btn" data-mihomo-close data-i18n="common.cancel">Cancel</button><button type="submit" class="primary-btn" data-i18n="mh.applyToDraft">Apply to Draft</button></footer></form></section>`;
    document.body.appendChild(ruleModal);
    $$('[data-mihomo-close]').forEach(button=>button.onclick=()=>closeModal(button.closest('.modal-backdrop')));
    for(const modal of [outboundModal,ruleModal])modal.onclick=event=>{if(event.target===modal)closeModal(modal);};
    $('#mihomo-outbound-form').onsubmit=saveOutboundDraft;
    $('#mihomo-outbound-form').elements.kind.onchange=syncOutboundForm;
    $('#mihomo-outbound-form').elements.type.onchange=syncOutboundForm;
    $('#mihomo-outbound-form').elements.network.onchange=syncOutboundForm;
    $('#mihomo-outbound-form').elements.snellVersion.onchange=syncSnellOutboundForm;
    $('#mihomo-outbound-form').elements.snellObfsMode.onchange=syncSnellOutboundForm;
    $('#mihomo-outbound-form').elements.proxies.onchange=()=>syncGroupDefaultOptions();
    $$('[data-outbound-mode]').forEach(button=>button.onclick=()=>switchOutboundMode(button.dataset.outboundMode));
    $('#outbound-parse-link').onclick=parseOutboundLink;
    $('#outbound-share-link').onkeydown=event=>{if(event.key==='Enter'){event.preventDefault();parseOutboundLink();}};
    $('#outbound-yaml').oninput=()=>{syncYAMLLines();showImportError('');};
    $('#outbound-yaml').onscroll=()=>{$('#outbound-yaml-lines').scrollTop=$('#outbound-yaml').scrollTop;};
    for(const eventName of ['input','change'])$('#outbound-form-panel').addEventListener(eventName,()=>{outboundFormChanged=true;showImportError('');});
    $('#mihomo-generate-uuid').onclick=async()=>{try{const value=await api('/api/tools/uuid',{method:'POST'});$('#mihomo-outbound-form').elements.uuid.value=value.uuid;outboundFormChanged=true;}catch(error){toast(error.message,true);}};
    $('#mihomo-rule-form').onsubmit=saveRuleDraft;
    $('#mihomo-rule-form').elements.type.onchange=syncRuleForm;
  }

  function setFormValue(form,name,value){const field=form.elements[name];if(!field)return;if(field.type==='checkbox')field.checked=!!value;else field.value=value??'';}
  function fillOutboundForm(item){
    const form=$('#mihomo-outbound-form');
    const yamlDraft=$('#outbound-yaml').value,linkDraft=$('#outbound-share-link').value;
    form.reset();form.elements.kind.value=item.kind||'proxy';populateOutboundTypes(form,item.type);syncOutboundTargets(item);
    for(const field of form.querySelectorAll('[name]')){
      if(['kind','type','proxies','defaultSelected'].includes(field.name))continue;
      setFormValue(form,field.name,item[field.name]??(field.type==='checkbox'?false:''));
    }
    form.elements.tls.value=String(!!item.tls);
    form.elements.network.value=item.network||'tcp';
    form.elements.vmessCipher.value=item.type==='vmess'?item.cipher||'auto':'auto';
    form.elements.openVpnProto.value=item.openVpnProto||'udp';
    form.elements.openVpnDev.value=item.openVpnDev||'tun';
    form.elements.openVpnCipher.value=item.openVpnCipher||'AES-128-GCM';
    form.elements.openVpnAuth.value=item.openVpnAuth||'SHA256';
    form.elements.snellVersion.value=String(item.snellVersion||1);
    for(const option of form.elements.proxies.options)option.selected=(item.proxies||[]).includes(option.value);
    syncOutboundForm();syncGroupDefaultOptions(item.defaultSelected||'');
    $('#outbound-yaml').value=yamlDraft;$('#outbound-share-link').value=linkDraft;syncYAMLLines();
  }
  async function openOutboundModal(id=''){
    if(draft.outbounds.find(item=>item.id===id)?.vpngate){await openVPNGateModal(id);return;}
    ensureModals();editingOutbound=id;
    editingOriginal=clone(draft.outbounds.find(candidate=>candidate.id===id)||{kind:'proxy',type:'ss',port:443,cipher:'aes-256-gcm',network:'tcp',udp:true,testUrl:draft.basics.outboundTestUrl||defaultTestURL,interval:300,strategy:'consistent-hashing'});
    fillOutboundForm(editingOriginal);showImportError('');$('#outbound-share-link').value='';setYAML('');
    outboundFormChanged=false;outboundYAMLCached=false;
    setOutboundMode('form');$('#mihomo-outbound-title').textContent=muiT(id?'mh.editOutbound':'mihomo.addOutbound');openModal('mihomo-outbound-modal');
    requestAnimationFrame(()=>{if(typeof syncInkBars==='function')syncInkBars();});
    if(editingOriginal.native){await switchOutboundMode('yaml',editingOriginal);}else $('#mihomo-outbound-form').elements.name.focus();
  }
  function populateOutboundTypes(form,selected=''){
    const options=form.elements.kind.value==='group'?groupTypes:proxyTypes;
    form.elements.type.innerHTML=options.map(([value,label])=>`<option value="${value}">${label}</option>`).join('');
    if(options.some(([value])=>value===selected))form.elements.type.value=selected;
  }
  function syncOutboundTargets(item={}){
    const form=$('#mihomo-outbound-form'),name=item.name||'',selectedDialer=item.dialerProxy||'';
    form.elements.dialerProxy.innerHTML=`<option value="">${muiT('inbounds.statusNone')}</option>${targetOptions(selectedDialer,false,name)}`;
    form.elements.proxies.innerHTML=targetOptions('',true,name);
    for(const member of item.proxies||[])if(!Array.from(form.elements.proxies.options).some(option=>option.value===member))form.elements.proxies.add(new Option(member,member));
    if(selectedDialer&&!Array.from(form.elements.dialerProxy.options).some(option=>option.value===selectedDialer))form.elements.dialerProxy.add(new Option(selectedDialer,selectedDialer,true,true));
  }
  function syncOutboundForm(){
    const form=$('#mihomo-outbound-form'),kind=form.elements.kind.value,previous=form.elements.type.value;
    const allowed=kind==='group'?groupTypes:proxyTypes;if(!allowed.some(([value])=>value===previous))populateOutboundTypes(form);
    const type=form.elements.type.value,network=form.elements.network.value;
    $$('[data-kind]',form).forEach(field=>field.hidden=field.dataset.kind!==kind);
    $$('[data-types]',form).forEach(field=>field.hidden=kind!=='proxy'||!field.dataset.types.split(',').includes(type));
    $$('[data-network]',form).forEach(field=>field.hidden=kind!=='proxy'||!['vmess','vless','trojan'].includes(type)||field.dataset.network!==network);
    $$('[data-group-health]',form).forEach(field=>field.hidden=kind!=='group'||type==='select');
    $$('[data-group-type]',form).forEach(field=>field.hidden=kind!=='group'||field.dataset.groupType!==type);
    form.elements.server.required=kind==='proxy'&&type!=='direct';form.elements.port.required=kind==='proxy'&&type!=='direct';form.elements.proxies.required=kind==='group';
    for(const field of form.querySelectorAll('#outbound-form-panel [name]'))field.disabled=!!field.closest('[hidden]');
    syncSnellOutboundForm();
    syncGroupDefaultOptions();
  }
  function syncSnellOutboundForm(){
    const form=$('#mihomo-outbound-form');if(!form?.elements.snellVersion)return;
    const active=form.elements.kind.value==='proxy'&&form.elements.type.value==='snell',version=Number(form.elements.snellVersion.value)||1;
    if(!active)return;
    form.elements.snellReuse.disabled=version<4;if(version<4)form.elements.snellReuse.checked=false;
    form.elements.udp.disabled=version<3;if(version<3)form.elements.udp.checked=false;
    form.elements.snellObfsHost.disabled=!form.elements.snellObfsMode.value;
  }
  function syncGroupDefaultOptions(selected){
    const form=$('#mihomo-outbound-form'),members=Array.from(form.elements.proxies.selectedOptions).map(option=>option.value),current=selected??form.elements.defaultSelected.value;
    form.elements.defaultSelected.innerHTML=`<option value="">${muiT('mh.firstMember')}</option>${members.map(name=>`<option value="${esc(name)}">${esc(name)}</option>`).join('')}`;
    if(members.includes(current))form.elements.defaultSelected.value=current;
  }
  function orderedMembers(form){
    const selected=Array.from(form.elements.proxies.selectedOptions).map(option=>option.value);
    return [...(editingOriginal?.proxies||[]).filter(name=>selected.includes(name)),...selected.filter(name=>!(editingOriginal?.proxies||[]).includes(name))];
  }
  function readOutboundForm(){
    const form=$('#mihomo-outbound-form'),kind=form.elements.kind.value,type=form.elements.type.value;
    const csv=value=>String(value||'').split(',').map(part=>part.trim()).filter(Boolean);
    const csvInts=value=>csv(value).map(part=>Number(part)).filter(Number.isFinite);
    const item={...clone(editingOriginal||{}),id:form.elements.id.value||freshID('outbound'),kind,name:form.elements.name.value.trim(),type,server:type==='direct'?'':form.elements.server.value.trim(),port:type==='direct'?0:Number(form.elements.port.value)||0,username:form.elements.username.value.trim(),password:form.elements.password.value,snellPsk:form.elements.snellPsk.value,snellVersion:Number(form.elements.snellVersion.value)||1,snellReuse:form.elements.snellReuse.checked,snellObfsMode:form.elements.snellObfsMode.value,snellObfsHost:form.elements.snellObfsHost.value.trim(),uuid:form.elements.uuid.value.trim(),alterId:Number(form.elements.alterId.value)||0,cipher:type==='vmess'?form.elements.vmessCipher.value:form.elements.cipher.value,udp:form.elements.udp.checked,tls:form.elements.tls.value==='true',skipCertVerify:form.elements.skipCertVerify.checked,sni:form.elements.sni.value.trim(),alpn:form.elements.alpn.value.trim(),network:form.elements.network.value,wsPath:form.elements.wsPath.value.trim(),wsHost:form.elements.wsHost.value.trim(),grpcServiceName:form.elements.grpcServiceName.value.trim(),flow:form.elements.flow.value.trim(),encryption:form.elements.encryption.value.trim(),clientFingerprint:form.elements.clientFingerprint.value.trim(),ipVersion:form.elements.ipVersion.value,dialerProxy:form.elements.dialerProxy.value,interfaceName:form.elements.interfaceName.value.trim(),routingMark:Number(form.elements.routingMark.value)||0,tfo:form.elements.tfo.checked,mptcp:form.elements.mptcp.checked,wireGuardIp:form.elements.wireGuardIp.value.trim(),wireGuardIpv6:form.elements.wireGuardIpv6.value.trim(),wireGuardPrivateKey:form.elements.wireGuardPrivateKey.value.trim(),wireGuardPublicKey:form.elements.wireGuardPublicKey.value.trim(),wireGuardPreSharedKey:form.elements.wireGuardPreSharedKey.value.trim(),wireGuardReserved:csvInts(form.elements.wireGuardReserved.value),wireGuardPersistentKeepalive:Number(form.elements.wireGuardPersistentKeepalive.value)||0,wireGuardMtu:Number(form.elements.wireGuardMtu.value)||0,wireGuardWorkers:Number(form.elements.wireGuardWorkers.value)||0,wireGuardDns:csv(form.elements.wireGuardDns.value),wireGuardRemoteDnsResolve:form.elements.wireGuardRemoteDnsResolve.checked,wireGuardRefreshServerIpInterval:Number(form.elements.wireGuardRefreshServerIpInterval.value)||0,openVpnProto:form.elements.openVpnProto.value,openVpnDev:form.elements.openVpnDev.value,openVpnCipher:form.elements.openVpnCipher.value,openVpnDataCiphers:csv(form.elements.openVpnDataCiphers.value),openVpnDataCipherFallback:form.elements.openVpnDataCipherFallback.value.trim(),openVpnAuth:form.elements.openVpnAuth.value,openVpnCompLzo:form.elements.openVpnCompLzo.value,openVpnCa:form.elements.openVpnCa.value.trim(),openVpnCert:form.elements.openVpnCert.value.trim(),openVpnKey:form.elements.openVpnKey.value.trim(),openVpnTlsAuth:form.elements.openVpnTlsAuth.value.trim(),openVpnKeyDirection:form.elements.openVpnKeyDirection.value,openVpnTlsCrypt:form.elements.openVpnTlsCrypt.value.trim(),openVpnTlsCryptV2:form.elements.openVpnTlsCryptV2.value.trim(),openVpnPing:Number(form.elements.openVpnPing.value)||0,openVpnPingRestart:Number(form.elements.openVpnPingRestart.value)||0,openVpnHandshakeTimeout:Number(form.elements.openVpnHandshakeTimeout.value)||0,openVpnMtu:Number(form.elements.openVpnMtu.value)||0,openVpnDns:csv(form.elements.openVpnDns.value),openVpnRemoteDnsResolve:form.elements.openVpnRemoteDnsResolve.checked,up:form.elements.up.value.trim(),down:form.elements.down.value.trim(),obfs:form.elements.obfs.value,obfsPassword:form.elements.obfsPassword.value,token:form.elements.token.value.trim(),congestionController:form.elements.congestionController.value,udpRelayMode:form.elements.udpRelayMode.value,proxies:orderedMembers(form),testUrl:form.elements.testUrl.value.trim(),interval:Number(form.elements.interval.value)||0,timeout:Number(form.elements.timeout.value)||0,tolerance:Number(form.elements.tolerance.value)||0,strategy:form.elements.strategy.value,defaultSelected:form.elements.defaultSelected.value,disableUdp:form.elements.disableUdp.checked};
    return item;
  }
  function importRequest(extra){return {context:draft.outbounds,editingId:editingOutbound,...extra};}
  function showImportError(message){const box=$('#outbound-import-error');box.textContent=message;box.hidden=!message;}
  function setImportBusy(busy){outboundBusy=busy;$('#outbound-apply').disabled=busy;$('#outbound-parse-link').disabled=busy;$$('[data-outbound-mode]').forEach(button=>button.disabled=busy);}
  function setYAML(value){$('#outbound-yaml').value=value;outboundYAMLCached=true;syncYAMLLines();}
  function syncYAMLLines(){const lines=$('#outbound-yaml').value.split('\n').length;$('#outbound-yaml-lines').textContent=Array.from({length:lines},(_,index)=>index+1).join('\n');}
  function setOutboundMode(mode){
    outboundMode=mode;$('#outbound-form-panel').hidden=mode!=='form';$('#outbound-yaml-panel').hidden=mode!=='yaml';
    $$('[data-outbound-mode]').forEach(button=>button.setAttribute('aria-selected',String(button.dataset.outboundMode===mode)));
    if(typeof syncInkBars==='function')syncInkBars();
    if(mode==='form')syncOutboundForm();
  }
  async function switchOutboundMode(mode,initialItem){
    if(outboundBusy||outboundMode===mode)return;
    setImportBusy(true);showImportError('');
    try{
      if(mode==='yaml'){
        if(outboundYAMLCached&&!outboundFormChanged&&!initialItem){setOutboundMode('yaml');return;}
        const response=await api('/api/mihomo/outbound/parse',{method:'POST',body:JSON.stringify({outbound:initialItem||readOutboundForm()})});
        setYAML(response.yaml);outboundFormChanged=false;setOutboundMode('yaml');
      }else{
        const response=await api('/api/mihomo/outbound/parse',{method:'POST',body:JSON.stringify({yaml:$('#outbound-yaml').value,preview:true})});
        const item=response.outbounds?.length===1?response.outbounds[0]:null;
        const types=item?.kind==='group'?groupTypes:proxyTypes;
        if(item&&(!item.type||types.some(([type])=>type===item.type))&&(!item.network||['tcp','ws','grpc'].includes(item.network))){
          item.id=editingOutbound;editingOriginal=clone(item);fillOutboundForm(item);
        }
        outboundFormChanged=false;setOutboundMode('form');
      }
    }catch{
      // Incomplete YAML stays in its editor while navigation remains available.
      setOutboundMode(mode);
    }finally{setImportBusy(false);}
  }
  async function parseOutboundLink(){
    if(outboundBusy)return;setImportBusy(true);showImportError('');
    try{
      const response=await api('/api/mihomo/outbound/parse',{method:'POST',body:JSON.stringify(importRequest({link:$('#outbound-share-link').value}))});
      setYAML(response.yaml);
    }catch(error){showImportError(error.message);}finally{setImportBusy(false);}
  }
  function renameReferences(items,rules,oldName,newName){
    for(const candidate of items){
      candidate.proxies=(candidate.proxies||[]).map(name=>name===oldName?newName:name);
      if(candidate.defaultSelected===oldName)candidate.defaultSelected=newName;
      if(candidate.dialerProxy===oldName)candidate.dialerProxy=newName;
      if(candidate.native){
        if(Array.isArray(candidate.native.proxies))candidate.native.proxies=candidate.native.proxies.map(name=>name===oldName?newName:name);
        for(const key of ['default-selected','dialer-proxy'])if(candidate.native[key]===oldName)candidate.native[key]=newName;
      }
    }
    for(const rule of rules)if(rule.target===oldName)rule.target=newName;
  }
  async function saveOutboundDraft(event){
    event.preventDefault();if(outboundBusy)return;
    if(outboundMode==='form'&&!event.currentTarget.reportValidity())return;
    setImportBusy(true);showImportError('');
    try{
      let yaml=$('#outbound-yaml').value;
      if(outboundMode==='form'){
        const exported=await api('/api/mihomo/outbound/parse',{method:'POST',body:JSON.stringify({outbound:readOutboundForm()})});yaml=exported.yaml;
      }
      const response=await api('/api/mihomo/outbound/parse',{method:'POST',body:JSON.stringify(importRequest({yaml}))});
      const next=clone(draft),index=next.outbounds.findIndex(item=>item.id===editingOutbound);
      if(index>=0){
        const previous=next.outbounds[index],item=response.outbounds[0];item.id=editingOutbound;
        next.outbounds[index]=item;
        if(previous.name!==item.name)renameReferences(next.outbounds,next.routingRules,previous.name,item.name);
      }else next.outbounds.push(...response.outbounds);
      draft=next;closeModal($('#mihomo-outbound-modal'));markDirty();
    }catch(error){showImportError(error.message);}finally{setImportBusy(false);}
  }

  function rulePayloadHint(type){
    if(type.startsWith('DOMAIN'))return muiT(type==='DOMAIN'?'mh.hintDomainFull':'mh.hintDomain');
    if(type==='GEOSITE')return muiT('mh.hintGeosite');
    if(type.includes('CIDR'))return muiT('mh.hintCidr');
    if(type.includes('PORT'))return muiT('mh.hintPort');
    if(type==='NETWORK')return muiT('mh.hintNetwork');
    if(type==='IN-NAME')return muiT('mh.hintInName');
    if(type==='IN-USER')return muiT('mh.hintInUser');
    if(type.includes('PROCESS'))return muiT('mh.hintProcess');
    if(type==='GEOIP'||type==='SRC-GEOIP')return muiT('mh.hintGeoip');
    return muiT('mh.hintDefault');
  }
  function openRuleModal(id=''){
    ensureModals();editingRule=id;
    const form=$('#mihomo-rule-form'),item=draft.routingRules.find(rule=>rule.id===id)||{id:'',type:'DOMAIN-SUFFIX',payload:'',target:'DIRECT',noResolve:false};
    form.reset();form.elements.id.value=item.id||'';form.elements.type.value=item.type;form.elements.payload.value=item.payload||'';form.elements.target.innerHTML=targetOptions(item.target||'DIRECT');form.elements.target.value=item.target||'DIRECT';form.elements.noResolve.checked=!!item.noResolve;syncRuleForm();
    $('#mihomo-rule-title').textContent=muiT(id?'mh.editRule':'mh.addRule');openModal('mihomo-rule-modal');(item.type==='MATCH'?form.elements.target:form.elements.payload).focus();
  }
  function syncRuleForm(){
    const form=$('#mihomo-rule-form'),type=form.elements.type.value,isMatch=type==='MATCH',noResolve=['GEOIP','IP-ASN','IP-CIDR','IP-CIDR6','IP-SUFFIX'].includes(type);
    $('#mihomo-rule-payload-field').hidden=isMatch;form.elements.payload.required=!isMatch;$('#mihomo-no-resolve-field').hidden=!noResolve;if(!noResolve)form.elements.noResolve.checked=false;$('#mihomo-rule-hint').textContent=rulePayloadHint(type);
  }
  function saveRuleDraft(event){
    event.preventDefault();const form=event.currentTarget,type=form.elements.type.value,item={id:form.elements.id.value||freshID('rule'),type,payload:type==='MATCH'?'':form.elements.payload.value.trim(),target:form.elements.target.value,noResolve:type==='MATCH'?false:form.elements.noResolve.checked};
    if(type!=='MATCH'&&!item.payload){toast(muiT('mh.payloadRequired',{type}),true);return;}
    const duplicateMatch=draft.routingRules.some(rule=>rule.type==='MATCH'&&rule.id!==item.id);if(type==='MATCH'&&duplicateMatch){toast(muiT('mh.singleMatch'),true);return;}
    const index=draft.routingRules.findIndex(rule=>rule.id===item.id);
    if(index>=0)draft.routingRules[index]=item;else{const matchIndex=draft.routingRules.findIndex(rule=>rule.type==='MATCH');draft.routingRules.splice(matchIndex<0?draft.routingRules.length:matchIndex,0,item);}
    const match=draft.routingRules.find(rule=>rule.type==='MATCH');draft.routingRules=draft.routingRules.filter(rule=>rule.type!=='MATCH');if(match)draft.routingRules.push(match);else ensureFinalMatch();
    closeModal($('#mihomo-rule-modal'));markDirty();
  }


  function fillBasicsForm(){
    const form=$('#mihomo-basics-form');
    for(const field of form.querySelectorAll('[name]')){
      const value=draft.basics[field.name];
      if(field.type==='checkbox')field.checked=!!value;else field.value=value??'';
    }
    for(const field of $$('[data-basics-list]'))renderBasicsTags(field.dataset.basicsList);
    renderBasicsIndicators();
  }
  function renderBasicsIndicators(){
    $('#basics-mode-warning').hidden=draft.basics.mode==='rule';
    $('#basics-dns-log').checked=draft.basics.logLevel==='debug';
  }
  function renderBasicsTags(name){
    const box=$('[data-basics-tags="'+name+'"] [data-tag-items]');
    box.innerHTML=(draft.basics[name]||[]).map((value,index)=>'<span class="basics-tag">'+esc(value)+'<button type="button" data-remove-tag="'+index+'" aria-label="'+esc(muiT('mh.removeTag',{value:value}))+'">'+SF('close')+'</button></span>').join('');
    $$('[data-remove-tag]',box).forEach(button=>button.onclick=()=>{draft.basics[name].splice(Number(button.dataset.removeTag),1);renderBasicsTags(name);markDirty();});
  }
  function commitBasicsTags(input){
    const items=input.value.split(/[,\n]+/).map(value=>value.trim()).filter(Boolean);
    if(!items.length)return;
    const name=input.dataset.basicsList;
    draft.basics[name]=[...new Set([...(draft.basics[name]||[]),...items])];
    input.value='';renderBasicsTags(name);markDirty();
  }
  function setupBasics(){
    const form=$('#mihomo-basics-form');
    form.onsubmit=event=>{event.preventDefault();saveSettings();};
    for(const eventName of ['input','change'])form.addEventListener(eventName,event=>{
      const field=event.target;
      if(field.name in draft.basics){
        draft.basics[field.name]=field.type==='checkbox'?field.checked:field.type==='number'?Number(field.value):field.value;
        markDirty();
      }else if(field.dataset.basicsList)markDirty();
    });
    for(const field of $$('[data-basics-list]')){
      field.onkeydown=event=>{if(event.key==='Enter'||event.key===','){event.preventDefault();commitBasicsTags(field);}};
      field.onblur=()=>commitBasicsTags(field);
    }
    $('#basics-reset').onclick=()=>{
      if(!confirm(muiT('mh.confirmResetBasics')))return;
      draft.basics=defaultBasics();for(const field of $$('[data-basics-list]'))field.value='';fillBasicsForm();markDirty();
    };
  }
  async function testOutbound(button){
    const name=button.dataset.testOutbound;
    button.disabled=true;delayResults.set(name,'Testing');
    renderOutbounds();
    try{
      const result=await api('/api/mihomo/outbound/delay',{method:'POST',body:JSON.stringify({name})});
      delayResults.set(name,result.delay+' ms');
    }catch(error){delayResults.set(name,error.message);}finally{if(currentTab==='outbounds')renderOutbounds();}
  }


  function currentWarpOutbound(){
    return draft.outbounds.find(item=>item.type==='wireguard'&&item.warpDeviceId)||draft.outbounds.find(item=>item.type==='wireguard'&&item.name==='warp');
  }
  function renderWarpRouting(){
    const item=currentWarpOutbound(),tags=$('[data-basics-tags="warpDomains"]');
    $('#basics-warp').hidden=!!item;
    tags.hidden=!item&&!(draft.basics.warpDomains||[]).length;
  }
  function ensureWarpModal(){
    if($('#warp-modal'))return;
    const modal=document.createElement('div');modal.id='warp-modal';modal.className='modal-backdrop';
    modal.innerHTML=`<section class="modal-panel warp-modal" role="dialog" aria-modal="true" aria-labelledby="warp-title">
      <header class="modal-titlebar"><span id="warp-title">Cloudflare WARP</span><button type="button" class="modal-close" id="warp-close" aria-label="Close" data-i18n-aria="common.close">${SF('close')}</button></header>
      <div class="modal-content">
        <div id="warp-loading" role="status" hidden data-i18n="mh.warpLoading">Loading...</div>
        <div id="warp-error" role="alert" hidden></div>
        <div id="warp-empty"><button class="outline-btn" id="warp-create" type="button" data-i18n="common.create">Create</button><p class="warp-note" data-i18n-html="mh.warpNote">Create 会向 Cloudflare 注册 WARP 设备并接受 <a href="https://www.cloudflare.com/application/terms/" target="_blank" rel="noreferrer">WARP 服务条款</a>。</p></div>
        <div id="warp-details" hidden>
          <table class="warp-info"><tbody>
            <tr><th data-i18n="mh.accessToken">Access Token</th><td id="warp-access-token"></td></tr><tr><th data-i18n="mh.deviceId">Device ID</th><td id="warp-device-id"></td></tr>
            <tr><th data-i18n="mh.licenseKey">License Key</th><td id="warp-license-value"></td></tr><tr><th data-i18n="drawer.privateKey">Private Key</th><td id="warp-private-key"></td></tr>
          </tbody></table>
          <button class="danger-btn" id="warp-delete" type="button" data-i18n="common.delete">Delete</button>
          <h3 class="warp-divider" data-i18n="mh.warpSettings">Settings</h3>
          <details class="basics-section warp-license"><summary>WARP/WARP+ License Key</summary><form id="warp-license-form" autocomplete="off"><label for="warp-license-input">Key</label><input id="warp-license-input" autocomplete="off" spellcheck="false"><button id="warp-license-update" type="submit" class="outline-btn" disabled data-i18n="common.update">Update</button></form></details>
          <h3 class="warp-divider" data-i18n="mh.warpAccountInfo">Account Information</h3>
          <button class="mihomo-primary" id="warp-refresh" type="button" data-i18n="mh.warpMoreInfo">More Information</button>
          <table class="warp-info"><tbody>
            <tr><th data-i18n="mh.deviceName">Device Name</th><td id="warp-device-name"></td></tr><tr><th data-i18n="mh.deviceModel">Device Model</th><td id="warp-device-model"></td></tr>
            <tr><th data-i18n="mh.deviceEnabled">Device Enabled</th><td id="warp-device-enabled"></td></tr><tr><th data-i18n="mh.accountType">Account Type</th><td id="warp-account-type"></td></tr>
            <tr><th data-i18n="mh.role">Role</th><td id="warp-account-role"></td></tr><tr><th data-i18n="mh.warpPlusData">WARP+ Data</th><td id="warp-premium"></td></tr>
            <tr><th data-i18n="mh.quota">Quota</th><td id="warp-quota"></td></tr><tr><th data-i18n="dash.usage">Usage</th><td id="warp-usage"></td></tr>
          </tbody></table>
          <div id="warp-config-error" role="status" hidden></div>
          <h3 class="warp-divider" data-i18n="mh.warpOutboundStatus">Outbound Status</h3>
          <div class="warp-outbound-status"><span class="mihomo-pill" id="warp-outbound-status"></span><span id="warp-pending" hidden data-i18n="mh.warpPending">Pending Save</span><button type="button" id="warp-add" class="mihomo-primary">Add Outbound</button></div>
        </div>
      </div>
    </section>`;
    document.body.appendChild(modal);
    $('#warp-close').onclick=()=>{if(!warpBusy)closeModal(modal);};
    modal.onclick=event=>{if(event.target===modal&&!warpBusy)closeModal(modal);};
    document.addEventListener('keydown',event=>{if(event.key==='Escape'&&!warpBusy&&modal.classList.contains('open'))closeModal(modal);});
    $('#warp-create').onclick=()=>warpAction('/create');
    $('#warp-refresh').onclick=()=>warpAction('/refresh');
    $('#warp-delete').onclick=deleteWarp;
    $('#warp-add').onclick=addWarpOutbound;
    $('#warp-license-input').oninput=syncWarpButtons;
    $('#warp-license-form').onsubmit=event=>{event.preventDefault();if(!$('#warp-license-update').disabled)warpAction('/license',{license:$('#warp-license-input').value.trim()});};
  }
  let vpngateData=null,vpngateBusy=false,vpngateTimer=null,vpngateEditingId='',vpngateFetching=false,vpngateFetchedAt=0;
  function vpnGateUsedSlots(){
    const original=(app.state?.state?.outbounds||[]).find(item=>item.id===vpngateEditingId)?.vpngate?.slot;
    return new Set([...(vpngateData?.usedSlots||[]).filter(slot=>slot!==original),...draft.outbounds.filter(item=>item.vpngate&&item.id!==vpngateEditingId).map(item=>item.vpngate.slot)]);
  }
  function vpnGatePending(item){
    const saved=(app.state?.state?.outbounds||[]).find(candidate=>candidate.id===item.id);
    return !saved?.vpngate||saved.name!==item.name||JSON.stringify(saved.vpngate)!==JSON.stringify(item.vpngate);
  }
  async function refreshVPNGateData(force=false){
    if(vpngateFetching||(!force&&Date.now()-vpngateFetchedAt<5000))return;
    vpngateFetching=true;
    try{vpngateData=await api('/api/mihomo/vpngate');vpngateFetchedAt=Date.now();if($('#vpngate-modal')?.classList.contains('open'))renderVPNGateModal();else if(currentTab==='outbounds')renderOutbounds();}
    catch(error){if($('#vpngate-modal')?.classList.contains('open'))showVPNGateError(error.message);}
    finally{vpngateFetching=false;}
  }
  function vpnGateCountryInfo(code){return (vpngateData?.presets||[]).find(item=>item.code===code)||null;}
  function validVPNGateCountry(code){return /^[A-Z]{2}$/.test(String(code||''));}
  function vpnGateStatusForSlot(slot){return (vpngateData?.statuses||[]).find(item=>Number(item.slot)===Number(slot))||null;}
  function vpnGateStateLabel(state){return muiT({'starting':'mh.vpngateStarting','searching':'mh.vpngateSearching','connecting':'mh.vpngateConnecting','connected':'mh.vpngateConnected','reconnecting':'mh.vpngateReconnecting','error':'mh.vpngateError','pending':'mh.vpngatePending'}[state]||'mh.vpngateUnknown');}
  function vpnGateStatusHTML(item){
    if(!item?.vpngate)return '';
    const status=vpnGatePending(item)?null:vpnGateStatusForSlot(item.vpngate.slot);
    if(!status)return `<div class="vpngate-row-status"><span class="mihomo-pill">${esc(muiT(vpnGatePending(item)?'mh.vpngatePending':'mh.vpngateWaiting'))}</span></div>`;
    const details=[status.node,status.isp||status.asn].filter(Boolean).join(' · ');
    return `<div class="vpngate-row-status"><span class="mihomo-pill ${status.connected?'group':''}">${esc(vpnGateStateLabel(status.state))}</span>${status.fallback?`<span class="mihomo-pill fallback">${esc(muiT('mh.vpngateFallback'))}</span>`:''}<span class="vpngate-row-detail">${esc(details||status.lastError||'—')}</span></div>`;
  }
  function scheduleVPNGatePoll(delay=1500){
    clearTimeout(vpngateTimer);vpngateTimer=setTimeout(async()=>{vpngateTimer=null;if(!$('#vpngate-modal')?.classList.contains('open'))return;try{vpngateData=await api('/api/mihomo/vpngate');renderVPNGateModal();if(vpngateData?.install?.running)scheduleVPNGatePoll(1000);else if(vpngateData?.statuses?.length)scheduleVPNGatePoll(5000);}catch(error){showVPNGateError(error.message);}},delay);
  }
  function showVPNGateError(message){const box=$('#vpngate-error');if(box){box.textContent=message;box.hidden=!message;}}
  function syncVPNGateForm(){
    const form=$('#vpngate-form');if(!form)return;
    const country=form.elements.country.value.trim().toUpperCase();form.elements.country.value=country;
	if(form.dataset.country&&form.dataset.country!==country)form.elements.isp.value='';
	form.dataset.country=country;
    const valid=validVPNGateCountry(country),info=vpnGateCountryInfo(country),isp=form.elements.isp;
    isp.disabled=!valid;
    const note=$('#vpngate-isp-note');if(note)note.textContent=!valid?muiT('mh.vpngateChooseCountry'):country==='KR'?muiT('mh.vpngateKoreaOnly'):info?.keyword?muiT('mh.vpngateKeywordHint'):muiT('mh.vpngateKeywordOnly');
    const slot=Number(form.elements.slot.value)||0,used=vpnGateUsedSlots();
    for(const option of form.elements.slot.options)option.disabled=used.has(Number(option.value));
    const preview=$('#vpngate-slot-preview');if(preview)preview.textContent=muiT('mh.vpngateSlotPreview',{iface:`vpn_vpn${slot}`,table:100+slot});
	const raw=isp.value.trim(),preset=(info?.isps||[]).find(item=>item.label.toLowerCase()===raw.toLowerCase()||item.id.toLowerCase()===raw.toLowerCase()),add=$('#vpngate-add');
	if(add)add.disabled=vpngateBusy||!valid||used.has(slot)||!raw||(country==='KR'&&!preset);
  }
  function vpnGateFormConfig(){
    const form=$('#vpngate-form'),country=form.elements.country.value.trim().toUpperCase(),raw=form.elements.isp.value.trim(),info=vpnGateCountryInfo(country),preset=(info?.isps||[]).find(item=>item.label.toLowerCase()===raw.toLowerCase()||item.id.toLowerCase()===raw.toLowerCase());
    return {country,isp:preset?.id||'',ispKeyword:preset?'':raw,slot:Number(form.elements.slot.value)||0,name:form.elements.name.value.trim()};
  }
  function resetVPNGateForm(){
    const form=$('#vpngate-form');if(!form)return;form.reset();form.dataset.originalSlot='';form.dataset.country='';vpngateEditingId='';
    const option=Array.from(form.elements.slot.options).find(candidate=>!vpnGateUsedSlots().has(Number(candidate.value)));if(option)form.elements.slot.value=option.value;
    syncVPNGateForm();
  }
  function renderVPNGateStatuses(){
    const box=$('#vpngate-status-list');if(!box)return;
    const saved=(draft.outbounds||[]).filter(item=>item.vpngate),bySlot=new Map(saved.map(item=>[Number(item.vpngate.slot),item])),statuses=vpngateData?.statuses||[];
    for(const status of statuses)if(!bySlot.has(Number(status.slot)))bySlot.set(Number(status.slot),{name:status.outbound||'VPNGate',vpngate:{slot:Number(status.slot),country:status.country||'',ispKeyword:status.target||''}});
    const rows=[...bySlot.entries()].sort((a,b)=>a[0]-b[0]);
    if(!rows.length){box.innerHTML=`<div class="mihomo-empty"><strong>${esc(muiT('mh.vpngateNoSaved'))}</strong><span>${esc(muiT('mh.vpngateNoSavedNote'))}</span></div>`;return;}
    box.innerHTML=rows.map(([slot,item])=>{const status=vpnGateStatusForSlot(slot),config=item.vpngate;return `<div class="vpngate-status-row"><div class="vpngate-status-main"><strong>${esc(item.name)}</strong><span>${esc(config.country)} · ${esc(vpnGateISPLabelForUI(config))} · vpn_vpn${slot} · table ${100+slot}</span></div><div class="vpngate-status-state"><span class="mihomo-pill ${status?.connected?'group':''}">${esc(vpnGateStateLabel(status?.state||'pending'))}</span>${status?.fallback?`<span class="mihomo-pill fallback">${esc(muiT('mh.vpngateFallback'))}</span>`:''}${status?.node?`<span class="vpngate-row-detail">${esc(status.node)}${status.isp?` · ${esc(status.isp)}`:''}</span>`:''}${status?.lastError?`<small class="vpngate-status-error">${esc(status.lastError)}</small>`:''}</div><div class="mihomo-actions"><button type="button" class="mihomo-icon" data-vpngate-edit="${esc(String(slot))}" title="${esc(muiT('action.edit'))}">${SF('edit')}</button><button type="button" class="outline-btn" data-vpngate-reconnect="${esc(String(slot))}" ${status?'':'disabled'}>${esc(muiT('mh.vpngateReconnect'))}</button>${item.id?`<button type="button" class="danger-btn" data-vpngate-uninstall="${esc(item.id)}">${esc(muiT('mh.vpngateUninstall'))}</button>`:''}</div></div>`;}).join('');
    $$('[data-vpngate-edit]',box).forEach(button=>button.onclick=()=>editVPNGateSlot(Number(button.dataset.vpngateEdit)));
    $$('[data-vpngate-reconnect]',box).forEach(button=>button.onclick=()=>reconnectVPNGate(Number(button.dataset.vpngateReconnect)));
	$$('[data-vpngate-uninstall]',box).forEach(button=>button.onclick=()=>stageVPNGateUninstall(button.dataset.vpngateUninstall));
  }
  function vpnGateISPLabelForUI(config){const info=vpnGateCountryInfo(config.country),preset=(info?.isps||[]).find(item=>item.id===config.isp);return preset?.label||config.ispKeyword||'—';}
  function renderVPNGateModal(){
    const modal=$('#vpngate-modal');if(!modal)return;
    const install=vpngateData?.install||{},installBox=$('#vpngate-install'),form=$('#vpngate-form'),button=$('#vpngate-install-button'),message=$('#vpngate-install-message'),error=$('#vpngate-install-error');
    if(message)message.textContent=install.installed?muiT('mh.vpngateInstalled',{version:install.version||''}):install.running?`${muiT('mh.vpngateInstalling')} ${install.message||''}`:install.reason||muiT('mh.vpngateNotInstalled');
    if(error){error.textContent=install.error||'';error.hidden=!install.error;}
    if(button){button.hidden=!!install.installed||!install.supported;button.disabled=vpngateBusy||!!install.running;button.textContent=install.running?muiT('mh.vpngateInstalling'):muiT('mh.vpngateInstall');}
    if(installBox)installBox.classList.toggle('installed',!!install.installed);
    if(form){form.hidden=!install.installed;syncVPNGateForm();}
    renderVPNGateStatuses();
    if(typeof muiApply==='function')muiApply(modal);
    if(vpngateData?.install?.running)scheduleVPNGatePoll(1000);
    if(currentTab==='outbounds')renderOutbounds();
  }
  function ensureVPNGateModal(){
    if($('#vpngate-modal'))return;
    const modal=document.createElement('div');modal.id='vpngate-modal';modal.className='modal-backdrop';
    modal.innerHTML=`<section class="modal-panel vpngate-modal" role="dialog" aria-modal="true" aria-labelledby="vpngate-title"><header class="modal-titlebar"><span id="vpngate-title" data-i18n="mh.vpngateTitle">VPNGate</span><button type="button" class="modal-close" id="vpngate-close" data-i18n-aria="common.close" aria-label="Close">${SF('close')}</button></header><div class="modal-content"><div id="vpngate-error" role="alert" hidden></div><div id="vpngate-install" class="vpngate-install-card"><div><strong data-i18n="mh.vpngateClient">VPNGate Client</strong><p id="vpngate-install-message"></p><p data-i18n="mh.vpngateInstallNote">Installs the pinned official SoftEther VPN Client after checking Linux dependencies. Read and accept the included SoftEther licence before use.</p></div><button type="button" class="primary-btn" id="vpngate-install-button">Install</button><div id="vpngate-install-error" class="vpngate-status-error" role="alert" hidden></div></div><form id="vpngate-form" hidden><div class="mihomo-form-grid"><label class="mihomo-field"><span data-i18n="mh.vpngateCountry">Country</span><input name="country" maxlength="2" minlength="2" pattern="[A-Z]{2}" autocomplete="off" spellcheck="false" style="text-transform:uppercase" placeholder="JP" required></label><label class="mihomo-field"><span data-i18n="mh.vpngateISP">ISP / ASN keyword</span><input name="isp" autocomplete="off" spellcheck="false" disabled required><small id="vpngate-isp-note"></small></label><label class="mihomo-field"><span data-i18n="mh.vpngateName">Outbound name</span><input name="name" maxlength="128" placeholder="vpngate-jp-0"></label><label class="mihomo-field"><span data-i18n="mh.vpngateSlot">VPNGate NIC index</span><select name="slot">${Array.from({length:10},(_,index)=>`<option value="${index}">${index}</option>`).join('')}</select><small id="vpngate-slot-preview"></small></label></div><footer class="modal-footer"><button type="button" class="outline-btn" id="vpngate-cancel" data-i18n="common.cancel">Cancel</button><button type="submit" class="primary-btn" id="vpngate-add" data-i18n="mh.vpngateAdd">Add to Draft</button></footer></form><h3 class="warp-divider" data-i18n="mh.vpngateStatus">Saved VPNGate Outbounds</h3><div id="vpngate-status-list"></div></div></section>`;
    document.body.appendChild(modal);
    if(typeof window.muiUpgradeCombobox==='function'){
      window.muiUpgradeCombobox(modal.querySelector('[name="country"]'),function(){
        const presets=vpngateData?.presets;
        if(presets&&presets.length){
          return presets.map(item=>{
            const label=item.code==='JP'?'Japan':item.code==='KR'?'Korea':item.code;
            return {value:item.code,label,sublabel:label};
          });
        }
        return [
          {value:'JP',label:'Japan',sublabel:'Japan'},
          {value:'KR',label:'Korea',sublabel:'Korea'}
        ];
      });
      window.muiUpgradeCombobox(modal.querySelector('[name="isp"]'),function(){
        const form=$('#vpngate-form');if(!form)return [];
        const country=form.elements.country.value.trim().toUpperCase();
        const info=vpnGateCountryInfo(country);
        return (info?.isps||[]).map(item=>({value:item.label,label:item.label}));
      });
    }
    $('#vpngate-close').onclick=()=>{if(!vpngateBusy){if(window.muiCloseSelects)window.muiCloseSelects();clearTimeout(vpngateTimer);closeModal(modal);}};
    $('#vpngate-cancel').onclick=()=>{if(!vpngateBusy){if(window.muiCloseSelects)window.muiCloseSelects();vpngateEditingId='';closeModal(modal);}};
    modal.onclick=event=>{if(event.target===modal&&!vpngateBusy){if(window.muiCloseSelects)window.muiCloseSelects();clearTimeout(vpngateTimer);closeModal(modal);}};
    $('#vpngate-install-button').onclick=installVPNGate;
    $('#vpngate-form').onsubmit=saveVPNGateDraft;
    $('#vpngate-form').elements.country.oninput=syncVPNGateForm;
    $('#vpngate-form').elements.country.onchange=syncVPNGateForm;
    $('#vpngate-form').elements.isp.oninput=syncVPNGateForm;
    $('#vpngate-form').elements.slot.onchange=syncVPNGateForm;
    document.addEventListener('keydown',event=>{if(event.key==='Escape'&&!vpngateBusy&&modal.classList.contains('open')){clearTimeout(vpngateTimer);closeModal(modal);}});
  }
  async function openVPNGateModal(id=''){
    ensureVPNGateModal();openModal('vpngate-modal');if(vpngateBusy)return;
    vpngateEditingId='';showVPNGateError('');resetVPNGateForm();vpngateBusy=true;renderVPNGateModal();
    try{vpngateData=await api('/api/mihomo/vpngate');vpngateFetchedAt=Date.now();resetVPNGateForm();renderVPNGateModal();const item=draft.outbounds.find(candidate=>candidate.id===id);if(item?.vpngate)editVPNGateSlot(item.vpngate.slot);scheduleVPNGatePoll();}catch(error){showVPNGateError(error.message);}finally{vpngateBusy=false;renderVPNGateModal();}
  }
  async function installVPNGate(){
    if(vpngateBusy)return;vpngateBusy=true;showVPNGateError('');renderVPNGateModal();
    try{vpngateData=await api('/api/mihomo/vpngate/install',{method:'POST'});renderVPNGateModal();}catch(error){showVPNGateError(error.message);}finally{vpngateBusy=false;renderVPNGateModal();}
  }
  function editVPNGateSlot(slot){
    const item=draft.outbounds.find(candidate=>candidate.vpngate&&Number(candidate.vpngate.slot)===slot);if(!item)return;
    const form=$('#vpngate-form');if(!form||form.hidden)return;vpngateEditingId=item.id;form.dataset.originalSlot=String(slot);form.dataset.country=item.vpngate.country;form.elements.country.value=item.vpngate.country;form.elements.isp.value=vpnGateISPLabelForUI(item.vpngate);form.elements.name.value=item.name||'';form.elements.slot.value=String(slot);syncVPNGateForm();form.elements.country.focus();
  }
  async function reconnectVPNGate(slot){
    if(vpngateBusy)return;vpngateBusy=true;showVPNGateError('');try{vpngateData=await api('/api/mihomo/vpngate/reconnect',{method:'POST',body:JSON.stringify({slot})});renderVPNGateModal();scheduleVPNGatePoll(1000);}catch(error){showVPNGateError(error.message);}finally{vpngateBusy=false;renderVPNGateModal();}
  }
	function stageVPNGateUninstall(id){
	  const item=draft.outbounds.find(candidate=>candidate.id===id&&candidate.vpngate);if(!item)return;
	  const dependencies=outboundDependencies(item.name);
	  for(const inbound of app.state?.state?.inbounds||[])if(inbound.proxy===item.name)dependencies.push('Inbound '+inbound.name);
	  if(dependencies.length){showVPNGateError(muiT('mh.cannotDelete',{deps:dependencies.join(muiT('common.listSep')),name:item.name}));return;}
	  if(!confirm(muiT('mh.vpngateConfirmUninstall',{name:item.name})))return;
	  draft.outbounds=draft.outbounds.filter(candidate=>candidate.id!==id);vpngateEditingId='';markDirty();
	  clearTimeout(vpngateTimer);closeModal($('#vpngate-modal'));toast(muiT('mh.vpngateUninstallDrafted'));
	}
  async function saveVPNGateDraft(event){
    event.preventDefault();if(vpngateBusy)return;
    const form=event.currentTarget,config=vpnGateFormConfig();
    if(!validVPNGateCountry(config.country)){showVPNGateError(muiT('mh.vpngateCountryInvalid'));return;}
    const info=vpnGateCountryInfo(config.country),preset=(info?.isps||[]).find(item=>item.id===config.isp);
    if(config.country==='KR'&&!preset){showVPNGateError(muiT('mh.vpngateKoreaOnly'));return;}
    if(!config.isp&&!config.ispKeyword){showVPNGateError(muiT('mh.vpngateISPRequired'));return;}
    if(config.name&&draft.outbounds.some(item=>item.id!==vpngateEditingId&&item.name===config.name)){showVPNGateError(muiT('mh.vpngateNameTaken'));return;}
    const used=vpnGateUsedSlots();if(used.has(config.slot)){showVPNGateError(muiT('mh.vpngateSlotUsed'));return;}
    vpngateBusy=true;showVPNGateError('');
    try{
      const item=clone(await api('/api/mihomo/vpngate/outbound',{method:'POST',body:JSON.stringify(config)})),next=clone(draft),editing=vpngateEditingId?next.outbounds.findIndex(candidate=>candidate.id===vpngateEditingId):-1;
      item.id=editing>=0?vpngateEditingId:freshID('vpngate');
      if(editing>=0){const previous=next.outbounds[editing];next.outbounds[editing]=item;if(previous.name!==item.name)renameReferences(next.outbounds,next.routingRules,previous.name,item.name);}else next.outbounds.push(item);
      draft=next;markDirty();vpngateEditingId='';form.dataset.originalSlot='';closeModal($('#vpngate-modal'));
    }catch(error){showVPNGateError(error.message);}finally{vpngateBusy=false;renderVPNGateModal();}
  }

  function showWarpError(message){const box=$('#warp-error');box.textContent=message;box.hidden=!message;}
  function syncWarpButtons(){
    for(const id of ['warp-create','warp-refresh','warp-delete','warp-close'])$('#'+id).disabled=warpBusy;
    $('#warp-license-input').disabled=warpBusy;
    $('#warp-license-update').disabled=warpBusy||!/^[A-Za-z0-9]{8}-[A-Za-z0-9]{8}-[A-Za-z0-9]{8}$/.test($('#warp-license-input').value.trim());
    $('#warp-add').disabled=warpBusy||!warpData?.outbound;
    $('#warp-loading').hidden=!warpBusy;
  }
  function renderWarpModal(){
    const account=warpData?.account,device=account?.device||{},info=device.account||{},item=currentWarpOutbound();
    $('#warp-empty').hidden=!!account;$('#warp-details').hidden=!account;
    const values={'access-token':account?.accessToken,'device-id':account?.deviceId,'license-value':account?.licenseKey,'private-key':account?.privateKey,'device-name':device.name,'device-model':device.model,'device-enabled':device.enabled,'account-type':info.account_type,'account-role':info.role,premium:fmt(info.premium_data||0),quota:fmt(info.quota||0),usage:fmt(info.usage||0)};
    for(const [id,value] of Object.entries(values))$('#warp-'+id).textContent=value===undefined||value===null||value===''?'—':String(value);
    $('#warp-outbound-status').textContent=muiT(item?'common.enabled':'inbounds.statusDisabled');
    $('#warp-outbound-status').classList.toggle('group',!!item);
    $('#warp-add').textContent=muiT(item?'mh.resetOutbound':'mihomo.addOutbound');
    $('#warp-pending').hidden=!dirty;
    $('#warp-config-error').textContent=warpData?.configError||'';$('#warp-config-error').hidden=!warpData?.configError;
    syncWarpButtons();
  }
  async function openWarpModal(){
    ensureWarpModal();openModal('warp-modal');if(warpBusy)return;
    warpData=null;showWarpError('');warpBusy=true;renderWarpModal();
    try{warpData=await api('/api/mihomo/warp');}catch(error){showWarpError(error.message);}finally{warpBusy=false;renderWarpModal();}
  }
  async function warpAction(action,input){
    if(warpBusy)return;warpBusy=true;showWarpError('');syncWarpButtons();
    try{
      warpData=await api('/api/mihomo/warp'+action,{method:'POST',...(input?{body:JSON.stringify(input)}:{})});
      if(action==='/license')$('#warp-license-input').value='';
    }catch(error){showWarpError(error.message);}finally{warpBusy=false;renderWarpModal();}
  }
  function addWarpOutbound(){
    if(warpBusy||!warpData?.outbound)return;
    const existing=currentWarpOutbound(),item=clone(warpData.outbound);
    if(!existing&&draft.outbounds.some(candidate=>candidate.name===item.name)){showWarpError(muiT('mh.warpNameTaken'));return;}
    if(existing){
      item.id=existing.id;item.name=existing.name;item.native.name=existing.name;
      draft.outbounds[draft.outbounds.findIndex(candidate=>candidate.id===existing.id)]=item;
    }else{item.id=freshID('warp');draft.outbounds.push(item);}
    markDirty();renderWarpRouting();closeModal($('#warp-modal'));
  }
  async function deleteWarp(){
    if(warpBusy)return;
    const existing=currentWarpOutbound();
    if(existing){
      const dependencies=outboundDependencies(existing.name).filter(value=>value!=='Basics / WARP Routing');
      for(const inbound of app.state?.state?.inbounds||[])if(inbound.proxy===existing.name)dependencies.push('Inbound '+inbound.name);
      if(dependencies.length){showWarpError(muiT('mh.warpRemoveRefs',{deps:dependencies.join(muiT('common.listSep'))}));return;}
    }
    if(!confirm(muiT('mh.confirmDeleteWarp')))return;
    warpBusy=true;showWarpError('');syncWarpButtons();
    try{
      warpData=await api('/api/mihomo/warp',{method:'DELETE'});
      if(existing){draft.outbounds=draft.outbounds.filter(item=>item.id!==existing.id);draft.basics.warpDomains=[];$('#basics-warpDomains-input').value='';renderBasicsTags('warpDomains');markDirty();}
      renderWarpRouting();
    }catch(error){showWarpError(error.message);}finally{warpBusy=false;renderWarpModal();}
  }

  async function saveSettings(){
    for(const field of $$('[data-basics-list]'))commitBasicsTags(field);
    const button=$('#mihomo-save');button.disabled=true;
    try{
      const response=await api('/api/mihomo/settings',{method:'PUT',body:JSON.stringify(draft)});
      dirty=false;root.classList.remove('mihomo-dirty');
      if(app.state?.state){app.state.state.outbounds=clone(draft.outbounds);app.state.state.routingRules=clone(draft.routingRules);app.state.state.mihomoBasics=clone(draft.basics);app.state.state.settings.mode=draft.basics.mode;app.state.state.settings.logLevel=draft.basics.logLevel;}
      toast(response.message||muiT('mh.settingsSaved'));await refresh();
    }catch(error){toast(error.message,true);}finally{syncButtons();}
  }

  ensureModals();
  $$('.mihomo-tab',root).forEach(button=>button.onclick=()=>setTab(button.dataset.mihomoTab));
  $('#mihomo-add-outbound').onclick=()=>openOutboundModal();
  $('#mihomo-add-rule').onclick=()=>openRuleModal();
  $('#mihomo-warp').onclick=openWarpModal;
  $('#mihomo-vpngate').onclick=()=>openVPNGateModal();
  $('#basics-warp').onclick=openWarpModal;
  $('#mihomo-save').onclick=saveSettings;
  $('#mihomo-restart').onclick=()=>{if(dirty){toast(muiT('mh.saveFirst'),true);return;}coreAction('restart');};

  const baseRenderState=renderState;
  renderState=function(){baseRenderState();syncFromState();if(app.currentView==='mihomo'&&currentTab==='outbounds'&&draft.outbounds.some(item=>item.vpngate))refreshVPNGateData();};
  const baseSetView=setView;
  setView=function(name){baseSetView(name);if(name==='mihomo'){render();if(draft.outbounds.some(item=>item.vpngate))refreshVPNGateData();}};
  window.addEventListener('mui-langchange',()=>{
    render();
    for(const field of $$('[data-basics-list]'))renderBasicsTags(field.dataset.basicsList);
    if($('#mihomo-outbound-modal')?.classList.contains('open'))$('#mihomo-outbound-title').textContent=muiT(editingOutbound?'mh.editOutbound':'mihomo.addOutbound');
    if($('#mihomo-rule-modal')?.classList.contains('open')){$('#mihomo-rule-title').textContent=muiT(editingRule?'mh.editRule':'mh.addRule');syncRuleForm();}
    if($('#warp-modal')?.classList.contains('open'))renderWarpModal();
    if($('#vpngate-modal')?.classList.contains('open'))renderVPNGateModal();
  });
  setupBasics();syncFromState();setTab('basics');
})();
