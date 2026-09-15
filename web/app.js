'use strict';

/* ---------------- i18n ---------------- */
const I18N = {
  zh: {
    tagline: '局域网内 P2P 互传文件',
    devices: '附近设备', send: '发送文件', incoming: '传输', history: '传输历史',
    settings: '设置', searching: '正在搜索局域网设备…',
    dropHint: '把文件或文件夹拖到这里', chooseFiles: '选择文件', chooseFolder: '选择文件夹',
    sendToNone: '请先选择目标设备', sendTo: '发送到：', cancel: '取消',
    connect: '连接',
    manualHint: '搜索不到设备（如路由器开启了 AP 隔离）？输入对方 IP:端口 手动连接：',
    deviceName: '设备名称', downloadDir: '保存目录', autoAccept: '自动接收（不询问，直接保存）',
    save: '保存', thisDevice: '本机', offline: '离线', manual: '手动',
    selfAddrHint: '在其他设备上访问以下地址，可手动连接本机：',
    noHistory: '暂无传输记录',
    noFiles: '请先选择要发送的文件',
    preparing: '正在等待对方确认…',
    rejected: '对方拒绝了本次传输', expired: '对方长时间未确认，已超时',
    sendOk: '发送成功', sendFail: '发送失败：',
    filesCount: (n) => `${n} 个文件`,
    received: '已保存', to: '发给', from: '来自',
    waiting: '等待对方确认', accepted: '已接受', receiving: '接收中', sending: '发送中',
    done: '完成', failed: '失败', rejectedShort: '已拒绝', expiredShort: '已过期',
    savedHint: '已保存到设备的 LanShare 目录',
    addedPeer: '已连接设备', addFail: '连接失败：',
    settingsSaved: '设置已保存', thisMachine: '这是本机',
  },
  en: {
    tagline: 'P2P file transfer over LAN',
    devices: 'Nearby Devices', send: 'Send Files', incoming: 'Transfers', history: 'History',
    settings: 'Settings', searching: 'Searching for LAN devices…',
    dropHint: 'Drag files or folders here', chooseFiles: 'Choose Files', chooseFolder: 'Choose Folder',
    sendToNone: 'Select a target device first', sendTo: 'Send to: ', cancel: 'Cancel',
    connect: 'Connect',
    manualHint: 'No devices found (e.g. AP isolation enabled)? Enter peer IP:port to connect manually:',
    deviceName: 'Device name', downloadDir: 'Download directory', autoAccept: 'Auto accept (no prompt)',
    save: 'Save', thisDevice: 'This device', offline: 'offline', manual: 'manual',
    selfAddrHint: 'Open this address on other devices to connect manually:',
    noHistory: 'No transfers yet',
    noFiles: 'Choose files to send first',
    preparing: 'Waiting for the other side to accept…',
    rejected: 'The peer rejected this transfer', expired: 'The peer did not respond in time',
    sendOk: 'Sent successfully', sendFail: 'Send failed: ',
    filesCount: (n) => `${n} file${n > 1 ? 's' : ''}`,
    received: 'Saved', to: 'to', from: 'from',
    waiting: 'Waiting for approval', accepted: 'Accepted', receiving: 'Receiving', sending: 'Sending',
    done: 'Done', failed: 'Failed', rejectedShort: 'Rejected', expiredShort: 'Expired',
    savedHint: 'Saved to the LanShare folder on that device',
    addedPeer: 'Device connected', addFail: 'Connect failed: ',
    settingsSaved: 'Settings saved', thisMachine: 'This is your own device',
  },
};

/* ---------------- state ---------------- */
const state = {
  lang: localStorage.getItem('lanshare-lang') ||
    (navigator.language && navigator.language.startsWith('zh') ? 'zh' : 'en'),
  info: null,
  peers: [],
  offers: [],
  history: [],
  selected: null,
  files: [],          // {file: File, rel: string}
  offerSpeed: {},     // id -> {t, bytes, speed}
  xhr: null,
};

const $ = (s) => document.querySelector(s);
const t = (k, ...args) => {
  const v = I18N[state.lang][k];
  return typeof v === 'function' ? v(...args) : v;
};

/* ---------------- helpers ---------------- */
async function api(path, opts = {}) {
  const res = await fetch(path, {
    headers: { 'Content-Type': 'application/json' },
    ...opts,
  });
  let data = null;
  try { data = await res.json(); } catch (_) { /* empty body */ }
  if (!res.ok) {
    throw new Error((data && data.error) || `HTTP ${res.status}`);
  }
  return data;
}

function toast(msg, type = '') {
  const el = document.createElement('div');
  el.className = `toast ${type}`;
  el.textContent = msg;
  $('#toasts').appendChild(el);
  setTimeout(() => el.remove(), 4200);
}

function fmtSize(n) {
  if (n == null) return '';
  if (n < 1024) return `${n} B`;
  const units = ['KB', 'MB', 'GB', 'TB'];
  let v = n;
  let i = -1;
  do { v /= 1024; i++; } while (v >= 1024 && i < units.length - 1);
  return `${v.toFixed(v >= 100 ? 0 : 1)} ${units[i]}`;
}

function fmtSpeed(bps) {
  if (!bps || bps <= 0) return '';
  return `${fmtSize(bps)}/s`;
}

function fmtTime(ts) {
  const d = new Date(ts * 1000);
  const today = new Date();
  const sameDay = d.toDateString() === today.toDateString();
  const hm = `${String(d.getHours()).padStart(2, '0')}:${String(d.getMinutes()).padStart(2, '0')}`;
  return sameDay ? hm : `${d.getMonth() + 1}/${d.getDate()} ${hm}`;
}

function escapeHtml(s) {
  return s.replace(/[&<>"']/g, (c) => ({
    '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;', "'": '&#39;',
  }[c]));
}

function hueFromId(id) {
  let h = 0;
  for (let i = 0; i < id.length; i++) h = (h * 31 + id.charCodeAt(i)) % 360;
  return h;
}

/* ---------------- i18n apply ---------------- */
function applyI18n() {
  document.querySelectorAll('[data-i18n]').forEach((el) => {
    const key = el.getAttribute('data-i18n');
    const v = I18N[state.lang][key];
    if (typeof v === 'string') el.textContent = v;
  });
  document.documentElement.lang = state.lang === 'zh' ? 'zh-CN' : 'en';
  $('#lang').value = state.lang;
}

/* ---------------- data refresh ---------------- */
async function refresh() {
  try {
    const [info, peers, offers] = await Promise.all([
      api('/api/info'), api('/api/peers'), api('/api/offers'),
    ]);
    state.info = info;
    state.peers = peers;
    state.offers = offers;
    renderSelf();
    renderPeers();
    renderOffers();
    if (!$('#settings-dialog').open) fillSettings();
  } catch (e) { /* server restarting */ }
}

async function refreshHistory() {
  try {
    state.history = await api('/api/history');
    renderHistory();
  } catch (e) { /* ignore */ }
}

/* ---------------- rendering ---------------- */
// 数据未变化时跳过重绘，避免列表节点被频繁替换（也利于点击稳定性）
const renderCache = {};

function renderSelf() {
  if (!state.info) return;
  $('#self-chip').textContent = `⭐ ${state.info.name}`;
}

function renderPeers() {
  const box = $('#peers');
  const key = state.lang + ':' + JSON.stringify({ p: state.peers, s: state.selected });
  if (renderCache.peers === key) return;
  renderCache.peers = key;
  if (!state.peers.length) {
    box.innerHTML = `<div class="empty">${escapeHtml(t('searching'))}</div>`;
    return;
  }
  box.innerHTML = '';
  for (const p of state.peers) {
    const el = document.createElement('div');
    el.className = 'peer' + (p.self ? ' self' : '') +
      (!p.self && !p.online ? ' offline' : '') +
      (state.selected === p.id ? ' selected' : '');
    const badges = [];
    if (p.self) badges.push(`<span class="badge self">${escapeHtml(t('thisDevice'))}</span>`);
    if (!p.self && p.manual) badges.push(`<span class="badge manual">${escapeHtml(t('manual'))}</span>`);
    if (!p.self && !p.online) badges.push(`<span class="badge offline">${escapeHtml(t('offline'))}</span>`);
    el.innerHTML = `
      <div class="avatar" style="background:linear-gradient(135deg,hsl(${hueFromId(p.id)},65%,55%),hsl(${(hueFromId(p.id) + 60) % 360},65%,45%))">${escapeHtml((p.name || '?')[0].toUpperCase())}</div>
      <div class="peer-info">
        <div class="peer-name">${escapeHtml(p.name)} ${badges.join('')}</div>
        <div class="peer-sub">${escapeHtml(p.os || '')} · ${escapeHtml(p.ip)}:${p.port}</div>
      </div>
      ${state.selected === p.id ? '<span class="tick">✓</span>' : ''}`;
    if (!p.self) {
      el.addEventListener('click', () => {
        if (!p.online) { return; }
        state.selected = state.selected === p.id ? null : p.id;
        renderPeers();
        updateSendButton();
      });
    }
    box.appendChild(el);
  }
  updateSendButton();
}

function updateSendButton() {
  const btn = $('#btn-send');
  const peer = state.peers.find((p) => p.id === state.selected);
  if (state.xhr) {
    btn.disabled = true;
    return;
  }
  btn.disabled = !peer || !state.files.length;
  btn.textContent = peer
    ? `${t('sendTo')}${peer.name} (${state.files.length})`
    : t('sendToNone');
}

const STATUS_I18N = {
  pending: 'waiting', accepted: 'accepted', receiving: 'receiving', sending: 'sending',
  done: 'done', failed: 'failed', rejected: 'rejectedShort', expired: 'expiredShort',
};

function offerSpeed(id, now, total) {
  const prev = state.offerSpeed[id];
  let speed = prev && prev.speed ? prev.speed : 0;
  if (prev && now > prev.t && total > prev.bytes) {
    const inst = (total - prev.bytes) / (now - prev.t);
    speed = speed ? speed * 0.6 + inst * 0.4 : inst;
  }
  state.offerSpeed[id] = { t: now, bytes: total, speed };
  return speed;
}

function renderOffers() {
  const card = $('#incoming-card');
  const box = $('#offers');
  const list = state.offers.filter((o) => o.dir === 'in' || o.status === 'sending');
  const sig = state.lang + ':' + JSON.stringify(list.map((o) => [o.id, o.status, o.received, o.error]));
  const shouldBeVisible = list.length > 0;
  const isVisible = !card.classList.contains('hidden');
  if (renderCache.offers === sig && isVisible === shouldBeVisible) return;
  renderCache.offers = sig;
  card.classList.toggle('hidden', !shouldBeVisible);
  box.innerHTML = '';
  for (const o of list) {
    const now = performance.now() / 1000;
    const speed = (o.status === 'receiving' || o.status === 'sending')
      ? offerSpeed(o.id, now, o.received) : 0;
    const pct = o.total > 0 ? Math.min(100, (o.received / o.total) * 100) : 0;
    const arrow = o.dir === 'in' ? '↓' : '↑';
    const dirClass = o.dir === 'in' ? 'in' : 'out';
    const title = o.dir === 'in'
      ? `${escapeHtml(t('from'))} <span class="peer-name">${escapeHtml(o.peer.name)}</span> · ${escapeHtml(t('filesCount', o.files.length))}`
      : `${escapeHtml(t('to'))} <span class="peer-name">${escapeHtml(o.peer.name)}</span> · ${escapeHtml(t('filesCount', o.files.length))}`;
    const showBar = ['receiving', 'sending', 'done'].includes(o.status);
    const bar = showBar ? `
      <div class="offer-progress">
        <div class="progress"><div class="bar ${o.status === 'done' ? 'done' : ''}" style="width:${o.status === 'done' ? 100 : pct}%"></div></div>
        <div class="progress-meta"><span>${fmtSize(o.received)} / ${fmtSize(o.total)}${speed ? ' · ' + fmtSpeed(speed) : ''}</span></div>
      </div>` : '';
    const err = o.error ? `<div class="error">${escapeHtml(o.error)}</div>` : '';
    const el = document.createElement('div');
    el.className = 'offer';
    el.innerHTML = `
      <div class="dir ${dirClass}">${arrow}</div>
      <div class="offer-main">
        <div class="offer-title">${title}</div>
        <div class="offer-sub">${escapeHtml(o.files.map((f) => f.name).slice(0, 3).join(', '))}${o.files.length > 3 ? ` +${o.files.length - 3}` : ''}</div>
        ${err}
      </div>
      <div class="offer-side">
        ${o.status === 'pending' ? `
          <button class="btn tiny primary" data-act="accept">${escapeHtml(t('accepted'))} ✓</button>
          <button class="btn tiny ghost" data-act="reject">${escapeHtml(t('rejectedShort'))} ✕</button>` : ''}
        <span class="status-chip ${o.status}">${escapeHtml(t(STATUS_I18N[o.status] || o.status))}</span>
      </div>
      ${bar}`;
    if (o.status === 'pending') {
      el.querySelector('[data-act=accept]').addEventListener('click', async () => {
        try { await api(`/api/offer/${o.id}/accept`, { method: 'POST' }); }
        catch (e) { toast(e.message, 'error'); }
        refresh();
      });
      el.querySelector('[data-act=reject]').addEventListener('click', async () => {
        try { await api(`/api/offer/${o.id}/reject`, { method: 'POST' }); }
        catch (e) { toast(e.message, 'error'); }
        refresh();
      });
    }
    box.appendChild(el);
  }
}

function renderHistory() {
  const box = $('#history');
  const sig = state.lang + ':' + JSON.stringify(state.history);
  if (renderCache.history === sig) return;
  renderCache.history = sig;
  if (!state.history.length) {
    box.innerHTML = `<div class="empty">${escapeHtml(t('noHistory'))}</div>`;
    return;
  }
  box.innerHTML = '';
  for (const h of state.history) {
    const el = document.createElement('div');
    el.className = `hist-item ${h.status}`;
    el.innerHTML = `
      <div class="dir ${h.dir}">${h.dir === 'in' ? '↓' : '↑'}</div>
      <div class="h-main">
        <div>${escapeHtml(h.files.length === 1 ? h.files[0] : `${h.files[0]} 等 ${h.files.length} 项`)}</div>
        <div class="h-sub">${h.dir === 'in' ? t('from') : t('to')} ${escapeHtml(h.peer)} · ${fmtSize(h.total)} · ${fmtTime(h.time)}</div>
      </div>
      <div class="h-status">${escapeHtml(t(STATUS_I18N[h.status] || h.status))}</div>`;
    box.appendChild(el);
  }
}

/* ---------------- file collection ---------------- */
function addFiles(items) {
  for (const it of items) {
    if (!state.files.some((f) => f.rel === it.rel && f.file.size === it.file.size)) {
      state.files.push(it);
    }
  }
  renderFileList();
}

function renderFileList() {
  const box = $('#file-list');
  box.innerHTML = '';
  state.files.forEach((f, i) => {
    const el = document.createElement('div');
    el.className = 'file-item';
    el.innerHTML = `
      <span class="fname" title="${escapeHtml(f.rel)}">${escapeHtml(f.rel)}</span>
      <span class="fsize">${fmtSize(f.file.size)}</span>
      <button class="rm" title="remove">✕</button>`;
    el.querySelector('.rm').addEventListener('click', () => {
      state.files.splice(i, 1);
      renderFileList();
    });
    box.appendChild(el);
  });
  updateSendButton();
}

function walkEntry(entry, prefix) {
  return new Promise((resolve) => {
    if (entry.isFile) {
      entry.file((f) => {
        addFiles([{ file: f, rel: prefix + f.name }]);
        resolve();
      }, resolve);
    } else if (entry.isDirectory) {
      const reader = entry.createReader();
      const batch = () => reader.readEntries(async (entries) => {
        if (!entries.length) { resolve(); return; }
        for (const e of entries) {
          await walkEntry(e, `${prefix}${entry.name}/`);
        }
        batch();
      }, resolve);
      batch();
    } else {
      resolve();
    }
  });
}

async function handleDrop(e) {
  e.preventDefault();
  $('#dropzone').classList.remove('dragover');
  const items = [...(e.dataTransfer.items || [])];
  const entries = items.map((i) => i.webkitGetAsEntry && i.webkitGetAsEntry()).filter(Boolean);
  if (!entries.length) {
    addFiles([...e.dataTransfer.files].map((f) => ({ file: f, rel: f.name })));
    return;
  }
  for (const en of entries) await walkEntry(en, '');
}

/* ---------------- send flow ---------------- */
let sending = false;

async function send() {
  if (sending || state.xhr) return;
  const peer = state.peers.find((p) => p.id === state.selected);
  if (!peer) { toast(t('sendToNone'), 'error'); return; }
  if (!state.files.length) { toast(t('noFiles'), 'error'); return; }

  sending = true;
  const btn = $('#btn-send');
  btn.disabled = true;
  btn.textContent = t('preparing');
  try {
    const prep = await api('/api/send/prepare', {
      method: 'POST',
      body: JSON.stringify({
        target: peer.id,
        files: state.files.map((f) => ({ name: f.rel, size: f.file.size })),
      }),
    });
    if (prep.status !== 'accepted') {
      toast(prep.status === 'rejected' ? t('rejected') : t('expired'), 'error');
      refresh();
      return;
    }
    await upload(peer, prep.offer);
  } catch (e) {
    toast(t('sendFail') + e.message, 'error');
    refresh();
  } finally {
    sending = false;
    updateSendButton();
  }
}

function upload(peer, offerId) {
  return new Promise((resolve, reject) => {
    const fd = new FormData();
    for (const f of state.files) fd.append('files', f.file, f.rel);

    const prog = $('#send-progress');
    const fill = $('#send-bar-fill');
    const text = $('#send-progress-text');
    prog.classList.remove('hidden');
    fill.style.width = '0%';
    fill.classList.remove('done');

    let lastT = 0; let lastLoaded = 0; let speed = 0;
    const xhr = new XMLHttpRequest();
    state.xhr = xhr;
    xhr.open('POST', `/api/send/transfer?offer=${encodeURIComponent(offerId)}&target=${encodeURIComponent(peer.id)}`);
    xhr.upload.addEventListener('progress', (e) => {
      if (!e.lengthComputable) return;
      const now = e.timeStamp / 1000;
      if (now > lastT) {
        const inst = (e.loaded - lastLoaded) / (now - lastT);
        speed = speed ? speed * 0.7 + inst * 0.3 : inst;
      }
      lastT = now; lastLoaded = e.loaded;
      const pct = (e.loaded / e.total) * 100;
      fill.style.width = `${pct}%`;
      text.textContent = `${fmtSize(e.loaded)} / ${fmtSize(e.total)} · ${fmtSpeed(speed)}`;
    });
    const finish = (ok, msg) => {
      state.xhr = null;
      prog.classList.add('hidden');
      if (ok) {
        fill.style.width = '100%';
        fill.classList.add('done');
        setTimeout(() => {
          state.files = [];
          renderFileList();
          $('#btn-send').textContent = t('sendOk') + ' ✓';
          setTimeout(updateSendButton, 1600);
        }, 350);
        toast(t('sendOk'), 'success');
        refreshHistory();
        resolve();
      } else {
        fill.classList.add('failed');
        setTimeout(() => fill.classList.remove('failed'), 1200);
        toast(t('sendFail') + (msg || ''), 'error');
        reject(new Error(msg));
      }
      refresh();
    };
    xhr.onload = () => {
      let msg = `HTTP ${xhr.status}`;
      try { msg = JSON.parse(xhr.responseText).error || msg; } catch (_) { /* */ }
      finish(xhr.status === 200, xhr.status === 200 ? '' : msg);
    };
    xhr.onerror = () => finish(false, 'network error');
    xhr.onabort = () => finish(false, t('cancel'));
    $('#btn-cancel').onclick = () => xhr.abort();
    xhr.send(fd);
  });
}

/* ---------------- settings dialog ---------------- */
function fillSettings() {
  if (!state.info || $('#settings-dialog').open) return;
  $('#set-name').value = state.info.name;
  $('#set-dir').value = state.info.download_dir;
  $('#set-auto').checked = !!state.info.auto_accept;
  const addrs = (state.info.addrs && state.info.addrs.length)
    ? state.info.addrs.map((ip) => `http://${ip}:${state.info.port}`).join('\n')
    : `http://127.0.0.1:${state.info.port}`;
  $('#self-addr').textContent = addrs;
}

async function saveSettings() {
  try {
    await api('/api/settings', {
      method: 'POST',
      body: JSON.stringify({
        name: $('#set-name').value.trim(),
        download_dir: $('#set-dir').value.trim(),
        auto_accept: $('#set-auto').checked,
      }),
    });
    toast(t('settingsSaved'), 'success');
    $('#settings-dialog').close();
    refresh();
  } catch (e) {
    toast(e.message, 'error');
  }
}

/* ---------------- manual peer ---------------- */
async function addManualPeer() {
  const addr = $('#manual-addr').value.trim();
  if (!addr) return;
  try {
    const res = await api('/api/peers/add', {
      method: 'POST',
      body: JSON.stringify({ address: addr }),
    });
    if (res.peer && res.peer.id === (state.info && state.info.id)) {
      toast(t('thisMachine'), 'error');
      return;
    }
    state.selected = res.peer.id;
    toast(`${t('addedPeer')}: ${res.peer.name}`, 'success');
    $('#manual-addr').value = '';
    refresh();
  } catch (e) {
    toast(t('addFail') + e.message, 'error');
  }
}

/* ---------------- init ---------------- */
function init() {
  applyI18n();

  $('#lang').addEventListener('change', (e) => {
    state.lang = e.target.value;
    localStorage.setItem('lanshare-lang', state.lang);
    applyI18n();
    renderPeers(); renderOffers(); renderHistory(); updateSendButton();
  });

  const dz = $('#dropzone');
  dz.addEventListener('dragover', (e) => { e.preventDefault(); dz.classList.add('dragover'); });
  dz.addEventListener('dragleave', () => dz.classList.remove('dragover'));
  dz.addEventListener('drop', handleDrop);
  dz.addEventListener('click', (e) => {
    if (e.target.closest('button')) return;
    $('#file-input').click();
  });

  $('#btn-files').addEventListener('click', () => $('#file-input').click());
  $('#btn-folder').addEventListener('click', () => $('#folder-input').click());
  $('#file-input').addEventListener('change', (e) => {
    addFiles([...e.target.files].map((f) => ({ file: f, rel: f.webkitRelativePath || f.name })));
    e.target.value = '';
  });
  $('#folder-input').addEventListener('change', (e) => {
    addFiles([...e.target.files].map((f) => ({ file: f, rel: f.webkitRelativePath || f.name })));
    e.target.value = '';
  });

  $('#btn-send').addEventListener('click', send);
  $('#btn-add-peer').addEventListener('click', addManualPeer);
  $('#manual-addr').addEventListener('keydown', (e) => { if (e.key === 'Enter') addManualPeer(); });

  $('#btn-settings').addEventListener('click', () => { fillSettings(); $('#settings-dialog').showModal(); });
  $('#btn-set-cancel').addEventListener('click', () => $('#settings-dialog').close());
  $('#btn-set-save').addEventListener('click', saveSettings);
  $('#self-addr').addEventListener('click', () => {
    navigator.clipboard.writeText($('#self-addr').textContent).then(() => toast('✓'));
  });

  refresh();
  refreshHistory();
  setInterval(refresh, 2000);
  setInterval(refreshHistory, 5000);
}

init();
