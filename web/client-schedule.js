/* 客户端的周期流量重置与到期自动续期（3x-ui 3.7+ 同款）
 *
 * 添加/编辑客户端窗口和入站表单里的内联客户端编辑器共用这里的字段。
 * clientScheduleMarkup(client) 生成字段，bindClientSchedule(root, client, rerender)
 * 把改动写回 client；切换周期或续期方式时调用 rerender 重新显示对应的字段。
 */
(() => {
  const periods = ['never', 'hourly', 'daily', 'weekly', 'monthly'];

  const renewMode = client => Number(client.renewDay) > 0 ? 'monthly' : Number(client.renewWeekday) > 0 ? 'weekly' : Number(client.renewDays) > 0 ? 'days' : 'off';

  const option = (value, label, selected) => `<option value="${esc(value)}"${selected ? ' selected' : ''}>${esc(label)}</option>`;

  window.clientScheduleMarkup = client => {
    const period = client.trafficReset || 'never';
    const mode = renewMode(client);
    const count = Number(client.renewCount) || 0;
    let html = `<div class="field"><label>${esc(muiT('sched.trafficReset'))}</label><select data-schedule-field="trafficReset">${periods.map(value => option(value, muiT('common.' + value), value === period)).join('')}</select></div>`;
    if (period === 'monthly') {
      html += `<div class="field"><label>${esc(muiT('sched.resetDay'))}</label><input type="number" min="1" max="31" data-schedule-field="trafficResetDay" value="${Number(client.trafficResetDay) || 1}"></div>`;
    }
    html += `<div class="field"><label title="${esc(muiT('sched.renewHint'))}">${esc(muiT('sched.renew'))}</label><select data-schedule-field="renewMode">${[
      ['off', muiT('sched.renewOff')], ['days', muiT('sched.renewDays')], ['monthly', muiT('common.monthly')], ['weekly', muiT('common.weekly')],
    ].map(([value, label]) => option(value, label, value === mode)).join('')}</select></div>`;
    if (mode === 'days') {
      html += `<div class="field"><label>${esc(muiT('sched.renewEvery'))}</label><input type="number" min="1" max="3650" data-schedule-field="renewDays" value="${Number(client.renewDays) || 30}"></div>`;
    } else if (mode === 'monthly') {
      html += `<div class="field"><label>${esc(muiT('sched.renewDay'))}</label><input type="number" min="1" max="31" data-schedule-field="renewDay" value="${Number(client.renewDay) || 1}"></div>`;
    } else if (mode === 'weekly') {
      html += `<div class="field"><label>${esc(muiT('sched.renewWeekday'))}</label><select data-schedule-field="renewWeekday">${[1, 2, 3, 4, 5, 6, 7].map(day => option(String(day), muiT('sched.weekday' + day), day === Number(client.renewWeekday))).join('')}</select></div>`;
    }
    if (mode !== 'off') {
      const label = muiT('sched.renewMax') + (count ? ` · ${muiT('sched.renewed', {n: count})}` : '');
      html += `<div class="field"><label>${esc(label)}</label><input type="number" min="0" data-schedule-field="renewMax" value="${Number(client.renewMax) || ''}" placeholder="${esc(muiT('sched.unlimited'))}"></div>`;
    }
    return html;
  };

  window.bindClientSchedule = (root, client, rerender) => {
    root.querySelectorAll('[data-schedule-field]').forEach(control => {
      const field = control.dataset.scheduleField;
      const update = () => {
        if (field === 'trafficReset') {
          client.trafficReset = control.value === 'never' ? '' : control.value;
          client.trafficResetDay = client.trafficReset === 'monthly' ? (Number(client.trafficResetDay) || 1) : 0;
          rerender();
        } else if (field === 'renewMode') {
          const keep = {days: Number(client.renewDays) || 30, monthly: Number(client.renewDay) || 1, weekly: Number(client.renewWeekday) || 1};
          client.renewDays = control.value === 'days' ? keep.days : 0;
          client.renewDay = control.value === 'monthly' ? keep.monthly : 0;
          client.renewWeekday = control.value === 'weekly' ? keep.weekly : 0;
          if (control.value === 'off') client.renewMax = 0;
          rerender();
        } else {
          const limits = {trafficResetDay: [1, 31], renewDays: [1, 3650], renewDay: [1, 31], renewWeekday: [1, 7], renewMax: [0, 100000]}[field];
          client[field] = Math.min(limits[1], Math.max(limits[0], Math.round(Number(control.value)) || limits[0]));
        }
      };
      control.addEventListener(control.tagName === 'SELECT' ? 'change' : 'input', update);
    });
  };

  // 只保留这些字段，用来从表单草稿合并回要保存的客户端。
  window.clientScheduleFields = client => ({
    trafficReset: client.trafficReset === 'never' ? '' : (client.trafficReset || ''),
    trafficResetDay: client.trafficReset === 'monthly' ? (Number(client.trafficResetDay) || 1) : 0,
    renewDays: Number(client.renewDays) || 0,
    renewDay: Number(client.renewDay) || 0,
    renewWeekday: Number(client.renewWeekday) || 0,
    renewMax: Number(client.renewMax) || 0,
  });
})();
