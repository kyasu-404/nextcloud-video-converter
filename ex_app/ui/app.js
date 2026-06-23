(() => {
const BASE = window.__PROXY_BASE__ || '';
const form = document.getElementById('convertForm');
const statusEl = document.getElementById('status');
const submitBtn = document.getElementById('submitBtn');
const closeBtn = document.getElementById('closeBtn');

const statusText = document.getElementById('status_text');
const progressContainer = document.getElementById('progress_container');
const progressFill = document.getElementById('progress_fill');
const contentWrap = document.getElementById('content_wrap');
const fileboxSelected = document.getElementById('filebox_selected');
const fileboxEmpty = document.getElementById('filebox_empty');
const pickFileBtn = document.getElementById('pickFileBtn');
const cancelBtn = document.getElementById('cancelBtn');
const notifBanner = document.getElementById('notif_banner');
const allowNotifBtn = document.getElementById('allowNotifBtn');

let mediaDuration = 0;
let isHDR = false;

// ── Notification permission banner ──
function updateNotifBanner() {
  if (!window.Notification || !notifBanner) return;
  if (Notification.permission === 'default') {
    notifBanner.style.display = 'flex';
  } else {
    notifBanner.style.display = 'none';
  }
}
updateNotifBanner();

if (allowNotifBtn) {
  allowNotifBtn.addEventListener('click', async () => {
    try {
      const perm = await Notification.requestPermission();
      updateNotifBanner();
      if (perm === 'granted') {
        new Notification('Уведомления включены', {
          body: 'Вы будете получать уведомления о статусе конвертации',
        });
      }
    } catch (e) {
      console.warn('Notification permission request failed:', e);
    }
  });
}

function value(id) {
  const el = document.getElementById(id);
  return el ? el.value : '';
}

function setStatus(text, kind = '', progress = null) {
  statusEl.style.display = 'block';
  statusText.textContent = text;
  statusEl.dataset.kind = kind || '';

  if (progress !== null && progress >= 0) {
    progressContainer.style.display = 'block';
    progressFill.style.width = progress + '%';
  } else {
    progressContainer.style.display = 'none';
    progressFill.style.width = '0%';
  }
}

function resetUI() {
  submitBtn.style.display = 'inline-block';
  submitBtn.disabled = false;
  cancelBtn.style.display = 'none';
  cancelBtn.disabled = false;
  cancelBtn.textContent = 'Отменить';
}

function formatBytes(bytes, decimals = 2) {
  if (!+bytes) return '0 Bytes';
  const k = 1024;
  const dm = decimals < 0 ? 0 : decimals;
  const sizes = ['Bytes', 'KB', 'MB', 'GB', 'TB'];
  const i = Math.floor(Math.log(bytes) / Math.log(k));
  return `${parseFloat((bytes / Math.pow(k, i)).toFixed(dm))} ${sizes[i]}`;
}

function formatDuration(seconds) {
  if (!seconds) return 'Неизвестно';
  const h = Math.floor(seconds / 3600);
  const m = Math.floor((seconds % 3600) / 60);
  const s = Math.floor(seconds % 60);
  if (h > 0) return `${h}ч ${m}м ${s}с`;
  if (m > 0) return `${m}м ${s}с`;
  return `${s}с`;
}

// ── UI Dynamics ──
const containerSelect = document.getElementById('container');
const fastStartWrap = document.getElementById('fast_start_wrap');
const qualityTypeSelect = document.getElementById('quality_type');
const crfWrap = document.getElementById('crf_wrap');
const bitrateWrap = document.getElementById('bitrate_wrap');
const qualityCrfSelect = document.getElementById('quality_crf');
const crfCustomWrap = document.getElementById('crf_custom_wrap');
const hdrModeSelect = document.getElementById('hdr_mode');
const tonemapWrap = document.getElementById('tonemap_wrap');
const fpsSelect = document.getElementById('fps_select');
const fpsCustomWrap = document.getElementById('fps_custom_wrap');
const enableAdvanced = document.getElementById('enable_advanced');
const advancedWrap = document.getElementById('advanced_wrap');

enableAdvanced.addEventListener('change', () => {
  advancedWrap.style.display = enableAdvanced.checked ? 'block' : 'none';
});

containerSelect.addEventListener('change', () => {
  const val = containerSelect.value;
  if (val === 'mp4' || val === 'mov') {
    fastStartWrap.style.display = 'flex';
  } else {
    fastStartWrap.style.display = 'none';
  }
});

qualityTypeSelect.addEventListener('change', () => {
  if (qualityTypeSelect.value === 'crf') {
    crfWrap.style.display = 'block';
    bitrateWrap.style.display = 'none';
    qualityCrfSelect.dispatchEvent(new Event('change'));
    document.getElementById('est_size_status').style.display = 'none';
  } else {
    crfWrap.style.display = 'none';
    crfCustomWrap.style.display = 'none';
    bitrateWrap.style.display = 'block';
    updateEstSize();
  }
});

qualityCrfSelect.addEventListener('change', () => {
  if (qualityCrfSelect.value === 'custom') {
    crfCustomWrap.style.display = 'block';
  } else {
    crfCustomWrap.style.display = 'none';
  }
});

document.getElementById('custom_bitrate').addEventListener('input', updateEstSize);

function updateEstSize() {
  if (qualityTypeSelect.value === 'bitrate' && mediaDuration > 0) {
    const kbps = parseInt(document.getElementById('custom_bitrate').value, 10);
    if (!isNaN(kbps) && kbps > 0) {
      // kbps to bytes: kbps * 1000 / 8 * duration
      const estBytes = (kbps * 1000 / 8) * mediaDuration;
      document.getElementById('est_size_val').textContent = formatBytes(estBytes);
      document.getElementById('est_size_status').style.display = 'block';
      return;
    }
  }
  document.getElementById('est_size_status').style.display = 'none';
}

hdrModeSelect.addEventListener('change', () => {
  if (hdrModeSelect.value === 'sdr' && isHDR) {
    tonemapWrap.style.display = 'block';
  } else {
    tonemapWrap.style.display = 'none';
  }
});

fpsSelect.addEventListener('change', () => {
  if (fpsSelect.value === 'custom') {
    fpsCustomWrap.style.display = 'block';
  } else {
    fpsCustomWrap.style.display = 'none';
  }
});

// ── Fetch Metadata ──
async function fetchMetadata(filePath) {
  const metaBox = document.getElementById('metadata_box');
  const metaLoad = document.getElementById('md_loading');
  const metaContent = document.getElementById('md_content');
  
  metaBox.style.display = 'block';
  metaLoad.style.display = 'block';
  metaContent.style.display = 'none';
  isHDR = false;
  mediaDuration = 0;

  try {
    const res = await fetch(`${BASE}/api/metadata`, {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ file_path: filePath })
    });
    if (!res.ok) throw new Error('Failed to load metadata');
    const data = await res.json();
    
    document.getElementById('md_res').textContent = (data.Width && data.Height) ? `${data.Width}x${data.Height}` : 'Неизвестно';
    document.getElementById('md_duration').textContent = formatDuration(data.DurationSeconds);
    document.getElementById('md_vcodec').textContent = data.VideoCodec || 'Неизвестно';
    document.getElementById('md_acodec').textContent = data.AudioCodec || 'Неизвестно';
    document.getElementById('md_size').textContent = data.Size ? formatBytes(data.Size) : 'Неизвестно';
    document.getElementById('md_bitrate').textContent = data.Bitrate ? `${Math.round(data.Bitrate / 1000)} kbps` : 'Неизвестно';
    document.getElementById('md_hdr').textContent = data.IsHDR ? 'Да' : 'Нет';
    
    isHDR = data.IsHDR;
    mediaDuration = data.DurationSeconds || 0;

    // Trigger change to show/hide tonemap if default is SDR
    hdrModeSelect.dispatchEvent(new Event('change'));

    metaLoad.style.display = 'none';
    metaContent.style.display = 'block';
  } catch (err) {
    metaLoad.textContent = 'Ошибка загрузки метаданных';
    console.error(err);
  }
}

// Handle empty state (opened from top menu)
if (!value('file_id') || value('file_id') === '{{FILE_ID}}') {
  contentWrap.style.display = 'none';
  fileboxEmpty.style.display = 'block';
} else {
  fetchMetadata(value('file_path'));
}

if (pickFileBtn) {
  pickFileBtn.addEventListener('click', () => {
    if (window.parent && window.parent.OC && window.parent.OC.dialogs) {
      window.parent.OC.dialogs.filepicker('Выберите видео для конвертации', function(path) {
        if (Array.isArray(path)) path = path[0];
        if (!path) return;

        const fileName = path.split('/').pop();

        document.getElementById('file_id').value = 'picked';
        document.getElementById('file_path').value = path;
        document.getElementById('file_name').value = fileName;

        document.getElementById('display_id').textContent = 'picked';
        document.getElementById('display_path').textContent = path;
        document.getElementById('display_name').textContent = fileName;

        contentWrap.style.display = 'grid';
        fileboxEmpty.style.display = 'none';
        
        fetchMetadata(path);
      });
    } else {
      alert('Ошибка: API выбора файлов Nextcloud недоступно.');
    }
  });
}

async function pollTask(taskId) {
  const start = Date.now();
  while (true) {
    const res = await fetch(`${BASE}/api/task/${encodeURIComponent(taskId)}`, {
      cache: 'no-store'
    });
    if (!res.ok) {
      throw new Error(`Task ${taskId} not found`);
    }
    const task = await res.json();
    const parts = [`${task.status}`];
    if (typeof task.progress === 'number') parts.push(`${task.progress}%`);
    if (task.message) parts.push(task.message);
    setStatus(parts.join(' · '), task.status, task.progress);

    if (task.status === 'Готово') {
      setStatus(`✓ Готово: ${task.remote_output || task.output_path || 'файл загружен'}`, 'done', null);
      resetUI();
      return;
    }
    if (task.status === 'Ошибка') {
      setStatus(`✗ Ошибка: ${task.error || task.message || 'неизвестно'}`, 'error', null);
      resetUI();
      return;
    }
    if (Date.now() - start > 1000 * 60 * 60 * 3) {
      setStatus('Превышен лимит ожидания (3 часа). Проверь журнал сервера.', 'error', null);
      resetUI();
      return;
    }
    await new Promise(resolve => setTimeout(resolve, 1500));
  }
}

let currentTaskId = null;

if (cancelBtn) {
  cancelBtn.addEventListener('click', async () => {
    if (!currentTaskId) return;
    cancelBtn.disabled = true;
    cancelBtn.textContent = 'Отменяем...';
    try {
      await fetch(`${BASE}/api/task/${encodeURIComponent(currentTaskId)}/cancel`, { method: 'POST' });
    } catch (e) {
      console.error('Cancel failed', e);
    }
  });
}

closeBtn.addEventListener('click', () => {
  if (window.parent !== window) {
    try { window.parent.postMessage({ type: 'video-converter-close' }, '*'); } catch {}
  }
  if (window.parent && window.parent.OC) {
    window.parent.location.href = window.parent.OC.generateUrl('/apps/files/');
  } else {
    window.close();
  }
});

form.addEventListener('submit', async (e) => {
  e.preventDefault();

  submitBtn.disabled = true;
  setStatus('Отправка задачи...', '', 0);

  let fpsVal = value('fps_select');
  if (fpsVal === 'custom') fpsVal = value('fps_custom');

  let crfVal = value('quality_crf');
  if (crfVal === 'custom') crfVal = value('custom_crf');

  const useAdv = enableAdvanced.checked;

  const payload = {
    file_id: value('file_id'),
    file_path: value('file_path'),
    file_name: value('file_name'),
    container: value('container'),
    video_codec: value('video_codec'),
    resolution: value('resolution'),
    quality_crf: value('quality_type') === 'crf' ? crfVal : 'bitrate',
    bitrate: value('quality_type') === 'bitrate' ? value('custom_bitrate') : '',
    hdr_mode: useAdv ? value('hdr_mode') : 'copy',
    tonemap: useAdv && isHDR && value('hdr_mode') === 'sdr' ? value('tonemap') : '',
    audio_codec: value('audio_codec'),
    audio_bitrate: value('audio_bitrate'),
    preset: useAdv ? value('preset') : 'medium',
    fps: useAdv ? fpsVal : 'copy',
    subtitles: useAdv ? (value('subtitles') === 'true') : true,
    metadata: useAdv ? value('metadata') : 'copy',
    fast_start: document.getElementById('fast_start').checked,
    delete_original: document.getElementById('delete_original').checked,
    bit_depth: useAdv ? value('bit_depth') : 'copy',
    requesttoken: (window.OC && window.OC.requestToken) ? window.OC.requestToken : '',
    user_id: (window.OC && window.OC.currentUser) ? window.OC.currentUser : ''
  };

  try {
    const res = await fetch(`${BASE}/api/convert`, {
      method: 'POST',
      headers: {
        'Content-Type': 'application/json',
        'Accept': 'application/json'
      },
      body: JSON.stringify(payload)
    });

    const data = await res.json();
    if (!res.ok) {
      throw new Error(data.error || data.message || `HTTP ${res.status}`);
    }

    currentTaskId = data.task_id;
    cancelBtn.style.display = 'inline-block';
    cancelBtn.disabled = false;
    cancelBtn.textContent = 'Отменить';
    submitBtn.style.display = 'none';

    setStatus('В очереди · 0% · Ожидание...', '', 0);
    await pollTask(data.task_id);
  } catch (err) {
    setStatus(`Ошибка запроса: ${err.message || err}`, 'error', null);
    resetUI();
  }
});
})();
