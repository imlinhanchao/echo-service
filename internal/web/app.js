/* Echo — 测试前端（原生 JS，无构建步骤） */
(() => {
  'use strict';

  // ---------------------------------------------------------------------------
  // DOM
  // ---------------------------------------------------------------------------
  const dom = {
    boot: document.getElementById('boot'),
    authView: document.getElementById('auth-view'),
    authTabs: document.getElementById('auth-tabs'),
    authLoginTab: document.getElementById('auth-login-tab'),
    authRegisterTab: document.getElementById('auth-register-tab'),
    authForm: document.getElementById('auth-form'),
    authEmail: document.getElementById('auth-email'),
    authPassword: document.getElementById('auth-password'),
    authError: document.getElementById('auth-error'),
    authSubmit: document.getElementById('auth-submit'),
    authFootnote: document.getElementById('auth-footnote'),
    appView: document.getElementById('app-view'),
    mainNav: document.getElementById('main-nav'),
    userEmail: document.getElementById('user-email'),
    main: document.getElementById('main'),
    toasts: document.getElementById('toasts'),
    modal: document.getElementById('modal'),
    modalTitle: document.getElementById('modal-title'),
    modalBody: document.getElementById('modal-body'),
    modalFoot: document.getElementById('modal-foot'),
  };

  // ---------------------------------------------------------------------------
  // Utilities
  // ---------------------------------------------------------------------------
  const $ = (selector, root = document) => root.querySelector(selector);
  const $$ = (selector, root = document) => Array.from(root.querySelectorAll(selector));

  function esc(value) {
    return String(value ?? '').replace(/[&<>"']/g, (ch) => ({
      '&': '&amp;',
      '<': '&lt;',
      '>': '&gt;',
      '"': '&quot;',
      "'": '&#39;',
    }[ch]));
  }

  function safeDecode(value) {
    try {
      return decodeURIComponent(value);
    } catch (err) {
      return value;
    }
  }

  function pad2(value) {
    return String(value).padStart(2, '0');
  }

  function fmtTime(value) {
    if (!value) return '—';
    const date = new Date(value);
    if (Number.isNaN(date.getTime())) return String(value);
    return date.toLocaleString('zh-CN', {
      year: 'numeric',
      month: '2-digit',
      day: '2-digit',
      hour: '2-digit',
      minute: '2-digit',
      second: '2-digit',
      hour12: false,
    });
  }

  function fmtBytes(value) {
    const number = Number(value);
    if (!Number.isFinite(number) || number < 0) return '—';
    if (number < 1024) return `${Math.round(number)} B`;
    const units = ['KB', 'MB', 'GB', 'TB'];
    let size = number;
    let index = -1;
    do {
      size /= 1024;
      index += 1;
    } while (size >= 1024 && index < units.length - 1);
    const digits = size >= 100 ? 0 : size >= 10 ? 1 : 2;
    return `${size.toFixed(digits)} ${units[index]}`;
  }

  function fmtDuration(value) {
    const total = Math.max(0, Math.round(Number(value) || 0));
    const hours = Math.floor(total / 3600);
    const minutes = Math.floor((total % 3600) / 60);
    const seconds = total % 60;
    if (hours > 0) return `${hours}:${pad2(minutes)}:${pad2(seconds)}`;
    return `${minutes}:${pad2(seconds)}`;
  }

  function fmtClock(seconds) {
    const total = Math.max(0, Math.floor(Number(seconds) || 0));
    const hours = Math.floor(total / 3600);
    const minutes = Math.floor((total % 3600) / 60);
    const secs = total % 60;
    if (hours > 0) return `${hours}:${pad2(minutes)}:${pad2(secs)}`;
    return `${minutes}:${pad2(secs)}`;
  }

  function fmtSpeed(value) {
    const speed = Number(value);
    return Number.isFinite(speed) ? `${speed.toFixed(2)}×` : '—';
  }

  function initialOf(title) {
    const text = String(title || '').trim();
    if (!text) return '书';
    return Array.from(text).slice(0, 2).join('');
  }

  function authorsText(authors) {
    const list = Array.isArray(authors) ? authors : authors ? [authors] : [];
    const cleaned = list.map((item) => String(item || '').trim()).filter(Boolean);
    return cleaned.length ? cleaned.join(' / ') : '未知作者';
  }

  function languageLabel(value) {
    const labels = { chinese: '中文', english: '英文' };
    return labels[String(value || '').toLowerCase()] || value || '语言未知';
  }

  function coverHTML(url, title, extraClass = '') {
    const className = `cover${extraClass ? ` ${extraClass}` : ''}`;
    const placeholder = `<span class="cover-ph">${esc(initialOf(title))}</span>`;
    if (!url) return `<div class="${className} cover-fallback">${placeholder}</div>`;
    return `<div class="${className}"><img src="${esc(url)}" alt="${esc(title ? `${title} 封面` : '书籍封面')}" loading="lazy" referrerpolicy="no-referrer" data-cover>${placeholder}</div>`;
  }

  function decorateCovers(root = document) {
    $$('img[data-cover]', root).forEach((img) => {
      const markFailed = () => {
        const wrap = img.closest('.cover');
        if (wrap) wrap.classList.add('cover-fallback');
      };
      img.addEventListener('error', markFailed, { once: true });
      if (img.complete && img.naturalWidth === 0) markFailed();
    });
  }

  function loadingHTML(text = '加载中…') {
    return `<div class="loading"><span class="spinner" aria-hidden="true"></span><span>${esc(text)}</span></div>`;
  }

  function emptyHTML(title, text, action = '') {
    return `<div class="empty"><h3>${esc(title)}</h3><p>${esc(text)}</p>${action}</div>`;
  }

  function errorHTML(message, action = '') {
    return `<div class="notice bad"><div><strong>加载失败</strong><div>${esc(message || '未知错误')}</div>${action ? `<div class="notice-actions">${action}</div>` : ''}</div></div>`;
  }

  function pagebarHTML(action, page, totalPages) {
    return `<div class="pagebar">
      <button type="button" class="btn small" data-action="${action}" data-page="${page - 1}" ${page <= 1 ? 'disabled' : ''}>上一页</button>
      <span class="page-info">第 ${page} / ${totalPages} 页</span>
      <button type="button" class="btn small" data-action="${action}" data-page="${page + 1}" ${page >= totalPages ? 'disabled' : ''}>下一页</button>
    </div>`;
  }

  function setFormError(element, message) {
    if (!element) return;
    element.textContent = message || '';
    element.hidden = !message;
  }

  function setBusy(button, busy, text = '处理中…') {
    if (!button) return;
    if (busy) {
      if (button.dataset.idleText === undefined) button.dataset.idleText = button.textContent;
      button.disabled = true;
      button.textContent = text;
    } else {
      button.disabled = false;
      if (button.dataset.idleText !== undefined) {
        button.textContent = button.dataset.idleText;
        delete button.dataset.idleText;
      }
    }
  }

  function providerLabel(value) {
    if (value === 'edge') return 'Edge';
    if (value === 'local') return '本地';
    return value || '未知';
  }

  function modelProvider(model) {
    if (!model) return '';
    if (model.provider) return model.provider;
    return String(model.id || '').toLowerCase().includes('edge') ? 'edge' : 'local';
  }

  const JOB_STATUS_LABELS = {
    queued: '排队中',
    running: '进行中',
    cancelling: '取消中',
    done: '已完成',
    failed: '失败',
    cancelled: '已取消',
  };

  const LIBRARY_STATUS_LABELS = {
    ready: '就绪',
    downloading: '下载中',
    pending: '等待中',
    failed: '失败',
  };

  const PROVIDER_STATUS_LABELS = {
    ok: '正常',
    needs_login: '需要重新登录',
    error: '异常',
  };

  function jobStatusLabel(status) {
    return JOB_STATUS_LABELS[status] || status || '未知';
  }

  function libraryStatusLabel(status) {
    return LIBRARY_STATUS_LABELS[status] || status || '未知';
  }

  function providerStatusLabel(status) {
    return PROVIDER_STATUS_LABELS[status] || status || '未知';
  }

  function tone(status) {
    switch (status) {
      case 'ok':
      case 'done':
      case 'ready':
      case 'success':
        return 'good';
      case 'running':
      case 'processing':
        return 'info';
      case 'queued':
      case 'pending':
      case 'cancelling':
      case 'needs_login':
      case 'warning':
        return 'warn';
      case 'failed':
      case 'error':
        return 'bad';
      default:
        return 'muted';
    }
  }

  function capabilityLabel(value) {
    const labels = {
      search: '搜索',
      download: '下载',
      login: '邮箱密码',
      cookies: 'Cookie',
      quota: '配额',
    };
    return labels[value] || value;
  }

  function credentialLabel(key) {
    const labels = {
      remix_userid: 'remix_userid（用户 ID）',
      remix_userkey: 'remix_userkey（用户密钥）',
    };
    return labels[key] || key;
  }

  function bookPayload(book) {
    return {
      provider: book.provider || '',
      ref: book.ref || '',
      title: book.title || '',
      authors: Array.isArray(book.authors) ? book.authors : [],
      year: book.year || '',
      publisher: book.publisher || '',
      language: book.language || '',
      format: book.format || '',
      size: book.size ?? '',
      isbn: book.isbn || '',
      cover_url: book.cover_url || '',
      description: book.description || '',
      source_url: book.source_url || '',
    };
  }

  // ---------------------------------------------------------------------------
  // Toasts
  // ---------------------------------------------------------------------------
  let lastToast = { message: '', at: 0 };

  function toast(message, type = 'info', duration) {
    if (!message) return;
    const now = Date.now();
    if (message === lastToast.message && now - lastToast.at < 1200) return;
    lastToast = { message, at: now };

    const icons = { success: '✓', error: '!', info: 'i' };
    const item = document.createElement('div');
    item.className = `toast ${type}`;
    item.setAttribute('role', type === 'error' ? 'alert' : 'status');
    item.innerHTML = `<span class="toast-icon" aria-hidden="true">${icons[type] || 'i'}</span><div class="toast-msg"></div><button type="button" class="toast-close" aria-label="关闭提示">×</button>`;
    item.querySelector('.toast-msg').textContent = String(message);
    while (dom.toasts.children.length >= 3) dom.toasts.firstElementChild.remove();
    dom.toasts.appendChild(item);

    const remove = () => {
      if (!item.isConnected) return;
      item.classList.add('leaving');
      window.setTimeout(() => item.remove(), 180);
    };
    item.querySelector('.toast-close').addEventListener('click', remove, { once: true });
    window.setTimeout(remove, duration ?? (type === 'error' ? 6500 : 3800));
  }

  // ---------------------------------------------------------------------------
  // API
  // ---------------------------------------------------------------------------
  class ApiError extends Error {
    constructor(status, code, message) {
      super(message || '请求失败');
      this.name = 'ApiError';
      this.status = status;
      this.code = code || '';
    }
  }

  function defaultStatusMessage(status) {
    const messages = {
      400: '请求参数有误',
      401: '未登录或登录已过期',
      403: '没有权限执行此操作',
      404: '请求的资源不存在',
      409: '操作冲突，请刷新后重试',
      429: '请求过于频繁，请稍后再试',
      500: '服务内部错误',
      502: '上游服务返回错误',
      503: '服务暂不可用',
    };
    return messages[status] || `请求失败（HTTP ${status}）`;
  }

  async function readError(response) {
    let code = '';
    let message = '';
    let registrationOpen;
    try {
      const text = await response.text();
      if (text) {
        try {
          const data = JSON.parse(text);
          if (data && data.error && typeof data.error === 'object') {
            code = data.error.code || '';
            message = data.error.message || '';
          } else if (data && typeof data.error === 'string') {
            message = data.error;
          }
          if (data && typeof data.registration_open === 'boolean') {
            registrationOpen = data.registration_open;
          }
        } catch (err) {
          if (text.length <= 300) message = text.trim();
        }
      }
    } catch (err) {
      // ignore unreadable error bodies
    }
    if (!message) message = defaultStatusMessage(response.status);
    return { code, message, registrationOpen };
  }

  const api = {
    async request(path, options = {}) {
      const {
        method = 'GET',
        body,
        quiet = false,
        handleAuth = true,
        headers = {},
      } = options;

      const init = {
        method,
        credentials: 'same-origin',
        headers: { Accept: 'application/json', ...headers },
      };
      if (body !== undefined) {
        init.headers['Content-Type'] = 'application/json';
        init.body = JSON.stringify(body);
      }

      let response;
      try {
        response = await fetch(path, init);
      } catch (err) {
        if (!quiet) toast('网络请求失败，请确认 Go 服务已启动', 'error');
        throw new ApiError(0, 'network', '网络请求失败');
      }

      if (response.status === 401) {
        const detail = await readError(response);
        if (handleAuth) handleUnauthorized();
        const error = new ApiError(401, detail.code, detail.message);
        if (typeof detail.registrationOpen === 'boolean') error.registrationOpen = detail.registrationOpen;
        throw error;
      }

      if (!response.ok) {
        const detail = await readError(response);
        if (!quiet) toast(detail.message || defaultStatusMessage(response.status), 'error');
        throw new ApiError(response.status, detail.code, detail.message);
      }

      if (options.raw) return response;
      if (response.status === 204) return {};
      const text = await response.text();
      if (!text) return {};
      try {
        return JSON.parse(text);
      } catch (err) {
        throw new ApiError(response.status, 'invalid_json', '服务返回了无法解析的数据');
      }
    },

    get(path, options = {}) {
      return api.request(path, { ...options, method: 'GET' });
    },

    post(path, body, options = {}) {
      return api.request(path, { ...options, method: 'POST', body });
    },

    put(path, body, options = {}) {
      return api.request(path, { ...options, method: 'PUT', body });
    },

    del(path, options = {}) {
      return api.request(path, { ...options, method: 'DELETE' });
    },

    async text(url, options = {}) {
      const { quiet = true } = options;
      let response;
      try {
        response = await fetch(url, {
          credentials: 'same-origin',
          headers: { Accept: 'text/plain, text/*;q=0.9, */*;q=0.1' },
        });
      } catch (err) {
        throw new ApiError(0, 'network', '网络请求失败');
      }
      if (response.status === 401) {
        const detail = await readError(response);
        handleUnauthorized();
        throw new ApiError(401, detail.code, detail.message);
      }
      if (!response.ok) {
        const detail = await readError(response);
        if (!quiet) toast(detail.message || defaultStatusMessage(response.status), 'error');
        throw new ApiError(response.status, detail.code, detail.message);
      }
      return response.text();
    },
  };

  // ---------------------------------------------------------------------------
  // State
  // ---------------------------------------------------------------------------
  const initialSearchForm = () => ({
    provider: 'zlib',
    q: '',
    ext: '',
    lang: '',
    year_from: '',
    year_to: '',
    order: 'popular',
  });

  const state = {
    user: null,
    registrationOpen: null,
    authMode: 'login',
    providers: [],
    providersLoaded: false,
    providersLoading: null,
    providersError: '',
    tab: '',
    navToken: 0,
    sessionEpoch: 0,
    audioEl: null,
    audiobookDetail: null,
    search: {
      form: initialSearchForm(),
      status: 'idle',
      data: null,
      page: 1,
      error: '',
      token: 0,
    },
    library: {
      items: [],
      loading: false,
      error: '',
      loaded: false,
    },
    jobs: {
      items: [],
      total: 0,
      page: 1,
      loading: false,
      error: '',
      fetching: false,
      timer: null,
      lastUpdated: null,
    },
    audiobooks: {
      items: [],
      loading: false,
      error: '',
      loaded: false,
    },
  };

  function resetData() {
    state.providers = [];
    state.providersLoaded = false;
    state.providersLoading = null;
    state.providersError = '';
    state.search = {
      form: initialSearchForm(),
      status: 'idle',
      data: null,
      page: 1,
      error: '',
      token: 0,
    };
    state.library = { items: [], loading: false, error: '', loaded: false };
    state.jobs = {
      items: [],
      total: 0,
      page: 1,
      loading: false,
      error: '',
      fetching: false,
      timer: null,
      lastUpdated: null,
    };
    state.audiobooks = { items: [], loading: false, error: '', loaded: false };
    state.audiobookDetail = null;
    state.tab = '';
  }

  // ---------------------------------------------------------------------------
  // Modal
  // ---------------------------------------------------------------------------
  const modalState = { seq: 0, onClose: null };

  function openModal({ title, body = '', footer = '', onMount, onClose }) {
    modalState.seq += 1;
    const seq = modalState.seq;
    dom.modalTitle.textContent = title || '提示';
    dom.modalBody.innerHTML = body;
    dom.modalFoot.innerHTML = footer;
    dom.modalFoot.hidden = !footer;
    modalState.onClose = typeof onClose === 'function' ? onClose : null;
    if (!dom.modal.open) dom.modal.showModal();
    dom.modalBody.scrollTop = 0;
    if (typeof onMount === 'function') onMount(dom.modalBody, dom.modalFoot);
    return seq;
  }

  function closeModal() {
    if (dom.modal.open) dom.modal.close();
  }

  function modalIsCurrent(seq) {
    return modalState.seq === seq && dom.modal.open;
  }

  function handleModalClosed() {
    dom.modalBody.innerHTML = '';
    dom.modalFoot.innerHTML = '';
    dom.modalFoot.hidden = true;
    dom.modalTitle.textContent = '提示';
    const callback = modalState.onClose;
    modalState.onClose = null;
    modalState.seq += 1;
    if (callback) callback();
  }

  function confirmDialog(message, { title = '确认操作', confirmText = '确定', danger = true } = {}) {
    return new Promise((resolve) => {
      let settled = false;
      const finish = (value) => {
        if (settled) return;
        settled = true;
        resolve(value);
      };
      openModal({
        title,
        body: `<p class="confirm-text">${esc(message)}</p>`,
        footer: `<button type="button" class="btn ghost" data-confirm-cancel>取消</button><button type="button" class="btn ${danger ? 'danger' : 'primary'}" data-confirm-ok>${esc(confirmText)}</button>`,
        onMount(_body, footer) {
          footer.querySelector('[data-confirm-cancel]').addEventListener('click', () => {
            finish(false);
            closeModal();
          });
          footer.querySelector('[data-confirm-ok]').addEventListener('click', () => {
            finish(true);
            closeModal();
          });
        },
        onClose() {
          finish(false);
        },
      });
    });
  }

  // ---------------------------------------------------------------------------
  // Auth
  // ---------------------------------------------------------------------------
  function setRegistrationOpen(value) {
    if (typeof value === 'boolean') state.registrationOpen = value;
  }

  function renderAuth() {
    const registering = state.authMode === 'register' && state.registrationOpen !== false;
    state.authMode = registering ? 'register' : 'login';
    dom.authLoginTab.classList.toggle('active', !registering);
    dom.authRegisterTab.classList.toggle('active', registering);
    dom.authLoginTab.setAttribute('aria-selected', String(!registering));
    dom.authRegisterTab.setAttribute('aria-selected', String(registering));
    dom.authRegisterTab.hidden = state.registrationOpen === false;
    dom.authSubmit.textContent = registering ? '注册并登录' : '登录';
    dom.authPassword.autocomplete = registering ? 'new-password' : 'current-password';
    dom.authFootnote.textContent = state.registrationOpen === false
      ? '注册已关闭，请联系管理员开通账号。'
      : registering
        ? '注册成功后会自动登录。'
        : '还没有账号？可切换到「注册」创建。';
  }

  function setAuthMode(mode) {
    if (mode === 'register' && state.registrationOpen === false) mode = 'login';
    state.authMode = mode;
    setFormError(dom.authError, '');
    renderAuth();
  }

  function showAuth() {
    state.navToken += 1;
    state.sessionEpoch += 1;
    state.user = null;
    stopJobPolling();
    stopAudio();
    closeModal();
    resetData();
    dom.authForm.reset();
    setFormError(dom.authError, '');
    dom.boot.hidden = true;
    dom.appView.hidden = true;
    dom.authView.hidden = false;
    dom.userEmail.textContent = '';
    document.title = '登录 · Echo';
    state.authMode = 'login';
    renderAuth();
    window.scrollTo(0, 0);
  }

  function showApp() {
    dom.boot.hidden = true;
    dom.authView.hidden = true;
    dom.appView.hidden = false;
    const email = state.user && state.user.email ? state.user.email : '';
    dom.userEmail.textContent = email;
    dom.userEmail.title = email ? `当前账号：${email}` : '当前账号';
  }

  function handleUnauthorized() {
    const wasLoggedIn = Boolean(state.user);
    showAuth();
    if (wasLoggedIn) toast('登录已过期，请重新登录', 'error');
  }

  async function submitAuth(event) {
    event.preventDefault();
    const mode = state.authMode;
    const email = dom.authEmail.value.trim();
    const password = dom.authPassword.value;
    setFormError(dom.authError, '');

    if (!email) {
      setFormError(dom.authError, '请输入邮箱');
      dom.authEmail.focus();
      return;
    }
    if (!password) {
      setFormError(dom.authError, '请输入密码');
      dom.authPassword.focus();
      return;
    }

    setBusy(dom.authSubmit, true, mode === 'register' ? '注册中…' : '登录中…');
    let switchToLogin = false;
    try {
      const data = await api.post(
        mode === 'register' ? '/api/auth/register' : '/api/auth/login',
        { email, password },
        { quiet: true, handleAuth: false },
      );
      let user = data && data.user;
      try {
        const me = await api.get('/api/auth/me', { quiet: true, handleAuth: false });
        if (me && me.user) user = me.user;
        if (me) setRegistrationOpen(me.registration_open);
      } catch (meError) {
        // The login/register call already succeeded; keep going with its user
        // if the follow-up session probe fails.
      }
      if (!user) throw new ApiError(500, 'invalid_response', '服务未返回用户信息');

      state.user = user;
      dom.authForm.reset();
      showApp();
      if (!location.hash || location.hash === '#') {
        try {
          history.replaceState(null, '', '#/search');
        } catch (err) {
          location.hash = '#/search';
        }
      }
      await router();
      toast(mode === 'register' ? '注册成功，已登录' : '登录成功', 'success');
    } catch (error) {
      const message = (error && error.message) || '请求失败';
      if (error && error.status === 401) {
        setFormError(dom.authError, mode === 'register' ? '注册失败：邮箱可能已被使用，或密码不符合要求' : '邮箱或密码错误');
      } else if (mode === 'register' && error && (error.code === 'registration_closed' || /registration[_-]?(closed|disabled)/i.test(error.code || ''))) {
        setRegistrationOpen(false);
        switchToLogin = true;
        setAuthMode('login');
        dom.authSubmit.dataset.idleText = '登录';
        setFormError(dom.authError, '注册已关闭，请联系管理员开通账号。');
      } else if (error && error.status === 403 && mode === 'register' && /关闭|disabled|closed/i.test(message)) {
        setRegistrationOpen(false);
        switchToLogin = true;
        setAuthMode('login');
        dom.authSubmit.dataset.idleText = '登录';
        setFormError(dom.authError, message);
      } else {
        setFormError(dom.authError, message);
      }
    } finally {
      setBusy(dom.authSubmit, false);
      if (switchToLogin) renderAuth();
    }
  }

  async function logout(button) {
    setBusy(button, true, '退出中…');
    try {
      await api.post('/api/auth/logout', {}, { quiet: true, handleAuth: false });
    } catch (err) {
      // the local session is cleared even if the server call fails
    } finally {
      setBusy(button, false);
    }
    showAuth();
    toast('已退出登录', 'info');
  }

  async function boot() {
    bindEvents();
    try {
      const me = await api.get('/api/auth/me', { quiet: true, handleAuth: false });
      setRegistrationOpen(me.registration_open);
      if (!me.user) throw new ApiError(401, 'unauthorized', '未登录');
      state.user = me.user;
      showApp();
      if (!location.hash || location.hash === '#') {
        try {
          history.replaceState(null, '', '#/search');
        } catch (err) {
          location.hash = '#/search';
        }
      }
      await router();
    } catch (error) {
      if (error && typeof error.registrationOpen === 'boolean') {
        setRegistrationOpen(error.registrationOpen);
      }
      if (error && error.status === 0) {
        toast('无法连接服务，请确认 Go 服务已启动', 'error');
      } else if (error && error.status !== 401) {
        toast((error && error.message) || '加载登录状态失败', 'error');
      }
      showAuth();
    }
  }

  // ---------------------------------------------------------------------------
  // Routing
  // ---------------------------------------------------------------------------
  const TAB_TITLES = {
    search: '搜索',
    library: '书库',
    jobs: '任务',
    audiobooks: '有声书',
    account: '账号',
  };

  function parseRoute() {
    const raw = (location.hash || '').replace(/^#\/?/, '');
    const parts = raw.split('/').filter(Boolean);
    if (!parts.length) return { tab: 'search' };
    const tab = parts[0];
    if (tab === 'audiobooks' && parts[1]) {
      return { tab: 'audiobooks', audiobookId: safeDecode(parts[1]) };
    }
    if (Object.prototype.hasOwnProperty.call(TAB_TITLES, tab)) return { tab };
    return { tab: 'search' };
  }

  function updateNav(tab) {
    $$('#main-nav a').forEach((link) => {
      const active = link.dataset.tab === tab;
      link.classList.toggle('active', active);
      if (active) link.setAttribute('aria-current', 'page');
      else link.removeAttribute('aria-current');
    });
  }

  function cleanupView() {
    stopJobPolling();
    stopAudio();
    if (dom.modal.open) closeModal();
  }

  async function router() {
    if (!state.user) {
      showAuth();
      return;
    }
    cleanupView();
    const token = ++state.navToken;
    const route = parseRoute();
    state.tab = route.tab;
    updateNav(route.tab);
    document.title = `${TAB_TITLES[route.tab] || ''} · Echo`;
    window.scrollTo(0, 0);

    try {
      if (route.tab === 'search') {
        await enterSearch(token);
      } else if (route.tab === 'library') {
        await enterLibrary(token);
      } else if (route.tab === 'jobs') {
        await enterJobs(token);
      } else if (route.tab === 'audiobooks') {
        if (route.audiobookId) await enterAudiobookDetail(route.audiobookId, token);
        else await enterAudiobooks(token);
      } else if (route.tab === 'account') {
        await enterAccount(token);
      }
    } catch (error) {
      if (token !== state.navToken || !state.user) return;
      dom.main.innerHTML = errorHTML((error && error.message) || '页面加载失败');
    }
  }

  function navigate(target) {
    if (location.hash === target) router();
    else location.hash = target;
  }

  // ---------------------------------------------------------------------------
  // Providers / account
  // ---------------------------------------------------------------------------
  function fallbackProviders() {
    return [{
      id: 'zlib',
      name: 'Z-Library',
      capabilities: ['search', 'download', 'login', 'cookies', 'quota'],
      default_domain: 'https://z-lib.gd',
      domains: ['https://z-lib.gd'],
      credential_keys: ['domain', 'remix_userid', 'remix_userkey'],
      account: null,
    }];
  }

  function providerList() {
    if (state.providersLoaded) return state.providers;
    return fallbackProviders();
  }

  function providerDomains(provider) {
    const domains = Array.isArray(provider.domains) ? provider.domains.filter(Boolean) : [];
    const list = domains.slice();
    if (provider.default_domain && !list.includes(provider.default_domain)) list.unshift(provider.default_domain);
    return list;
  }

  function accountModes(provider) {
    const capabilities = Array.isArray(provider.capabilities) ? provider.capabilities : [];
    const modes = [];
    if (capabilities.includes('login')) modes.push('password');
    if (capabilities.includes('cookies')) modes.push('cookies');
    if (!modes.length) modes.push('password');
    return modes;
  }

  function credentialKeys(provider) {
    const keys = Array.isArray(provider.credential_keys)
      ? provider.credential_keys.filter((key) => key && key !== 'domain')
      : [];
    if (keys.length) return keys;
    if (provider.id === 'zlib') return ['remix_userid', 'remix_userkey'];
    return [];
  }

  async function ensureProviders(force = false) {
    if (state.providersLoaded && !force) return state.providers;
    if (state.providersLoading) return state.providersLoading;
    const epoch = state.sessionEpoch;
    state.providersLoading = (async () => {
      try {
        const data = await api.get('/api/providers');
        if (epoch !== state.sessionEpoch) return [];
        state.providers = Array.isArray(data.providers) ? data.providers : [];
        state.providersLoaded = true;
        state.providersError = '';
        return state.providers;
      } catch (error) {
        if (epoch === state.sessionEpoch) {
          state.providersError = (error && error.message) || '图书源列表加载失败';
        }
        throw error;
      } finally {
        if (epoch === state.sessionEpoch) state.providersLoading = null;
      }
    })();
    return state.providersLoading;
  }

  function updateProviderAccount(providerId, account) {
    const provider = state.providers.find((item) => item.id === providerId);
    if (provider) provider.account = account || null;
  }

  function accountSummaryHTML(provider) {
    const account = provider.account;
    if (!account || !account.configured) {
      return `<div class="account-summary is-empty">
        <div class="account-line"><span class="badge muted">未配置</span><span>尚未配置该图书源账号</span></div>
        <p>配置账号凭据后才能使用该图书源的搜索与下载配额。</p>
      </div>`;
    }

    const status = account.status || 'unknown';
    const limit = Number(account.daily_limit) || 0;
    const used = Number(account.daily_used) || 0;
    const remaining = Number(account.daily_remaining);
    const hasQuota = account.quota_known === true && limit > 0;
    const percent = hasQuota ? Math.min(100, Math.round((used / limit) * 100)) : 0;
    let quotaText = '未知';
    let quotaSub = '服务端未返回配额信息';
    if (account.quota_known === true) {
      if (limit > 0) {
        quotaText = `${used} / ${limit}`;
        quotaSub = `剩余 ${Number.isFinite(remaining) ? remaining : Math.max(limit - used, 0)} 次`;
      } else {
        quotaText = `已用 ${used} 次`;
        quotaSub = '未提供每日上限';
      }
    }
    const hints = [];
    if (status === 'needs_login') hints.push('登录状态可能已失效，请重新验证或更新 Cookie。');
    if (status === 'error') hints.push('凭据验证失败，请更新凭据后重试。');

    return `<div class="account-summary">
      <div class="account-line">
        <span class="badge ${tone(status)}">${esc(providerStatusLabel(status))}</span>
        ${account.email ? `<span class="kv"><span>账号</span><strong>${esc(account.email)}</strong></span>` : '<span class="muted small">已配置凭据</span>'}
      </div>
      <div class="quota">
        <div class="quota-head"><span>今日下载配额</span><strong>${esc(quotaText)}</strong></div>
        ${hasQuota ? `<div class="progress small"><span style="width:${percent}%"></span></div>` : ''}
        <div class="quota-sub">${esc(quotaSub)}</div>
      </div>
      ${account.last_verified_at ? `<div class="kv"><span>最近验证</span><span>${esc(fmtTime(account.last_verified_at))}</span></div>` : ''}
      ${hints.map((hint) => `<p class="hint warn">${esc(hint)}</p>`).join('')}
    </div>`;
  }

  function providerCardHTML(provider) {
    const account = provider.account;
    const configured = Boolean(account && account.configured);
    const modes = accountModes(provider);
    const activeMode = modes[0];
    const keys = credentialKeys(provider);
    const domains = providerDomains(provider);
    const defaultDomain = provider.default_domain && domains.includes(provider.default_domain)
      ? provider.default_domain
      : domains[0] || '';
    const capabilities = Array.isArray(provider.capabilities) ? provider.capabilities : [];

    const domainField = domains.length
      ? `<label class="field"><span>域名</span><select name="domain">${domains.map((domain) => `<option value="${esc(domain)}" ${domain === defaultDomain ? 'selected' : ''}>${esc(domain)}</option>`).join('')}</select></label>`
      : '';

    const modeField = modes.length > 1
      ? `<div class="account-mode"><span>登录方式</span><div class="segmented">${modes.map((mode, index) => `<button type="button" class="${index === 0 ? 'active' : ''}" data-action="account-mode" data-mode="${mode}">${mode === 'cookies' ? 'Cookie 导入' : '邮箱密码'}</button>`).join('')}</div></div>`
      : '';

    const cookieFields = keys.length
      ? keys.map((key) => `<label class="field"><span>${esc(credentialLabel(key))}</span><input name="${esc(key)}" type="text" autocomplete="off" placeholder="从浏览器 Cookie 中复制"></label>`).join('')
      : '<p class="hint warn">该图书源未声明 Cookie 字段。</p>';

    return `<article class="provider-card" data-provider-card="${esc(provider.id)}">
      <div class="provider-head">
        <div class="provider-title"><h2>${esc(provider.name || provider.id)}</h2><span class="badge muted">${esc(provider.id)}</span></div>
        ${configured ? `<span class="badge ${tone(account.status)}">${esc(providerStatusLabel(account.status))}</span>` : '<span class="badge muted">未配置</span>'}
      </div>
      <div class="provider-caps">${capabilities.map((capability) => `<span class="chip">${esc(capabilityLabel(capability))}</span>`).join('')}</div>
      ${accountSummaryHTML(provider)}
      <form class="account-form" data-form="account" data-provider="${esc(provider.id)}">
        ${domainField}
        ${modeField}
        <div class="account-fields" data-account-fields="password" ${activeMode === 'password' ? '' : 'hidden'}>
          <label class="field"><span>邮箱</span><input name="email" type="email" autocomplete="username" placeholder="图书源登录邮箱" value="${account && account.email ? esc(account.email) : ''}"></label>
          <label class="field"><span>密码</span><input name="password" type="password" autocomplete="current-password" placeholder="图书源登录密码"></label>
        </div>
        <div class="account-fields" data-account-fields="cookies" ${activeMode === 'cookies' ? '' : 'hidden'}>
          ${cookieFields}
          <p class="help">浏览器登录图书源后，从开发者工具的 Cookie 中复制对应值；保存时后端会先验证。</p>
        </div>
        <p class="form-error" role="alert" hidden></p>
        <div class="form-actions">
          <button type="submit" class="btn primary">保存并验证</button>
          ${configured ? `<button type="button" class="btn" data-action="verify-account" data-provider-id="${esc(provider.id)}">验证账号</button>
          <button type="button" class="btn danger" data-action="delete-account" data-provider-id="${esc(provider.id)}">删除凭据</button>` : ''}
        </div>
      </form>
      <p class="provider-note">保存时后端会先验证凭据，验证通过后才会写入。</p>
    </article>`;
  }

  function renderAccountPage() {
    if (state.tab !== 'account') return;
    const head = `<div class="page-head">
      <div><h1>账号与凭据</h1><p class="sub">配置图书源账号后，才能使用搜索和下载配额。</p></div>
      <div class="page-actions"><button type="button" class="btn" data-action="refresh-account">刷新</button></div>
    </div>`;

    if (!state.providersLoaded) {
      dom.main.innerHTML = `${head}${errorHTML(state.providersError || '图书源列表加载失败', '<button type="button" class="btn small" data-action="retry-account">重试</button>')}`;
      return;
    }

    const warning = state.providersError
      ? `<div class="notice warn"><div>账号信息刷新失败：${esc(state.providersError)}。当前展示的是上次加载的数据。<button type="button" class="btn small" data-action="retry-account">重试</button></div></div>`
      : '';

    const content = state.providers.length
      ? `<div class="provider-grid">${state.providers.map((provider) => providerCardHTML(provider)).join('')}</div>`
      : emptyHTML('没有可用的图书源', '服务端尚未注册任何 provider。');

    dom.main.innerHTML = `${head}${warning}${content}`;
  }

  async function enterAccount(token) {
    if (!state.providersLoaded || state.providersError) {
      dom.main.innerHTML = loadingHTML('加载图书源与账号…');
      try {
        await ensureProviders(true);
      } catch (error) {
        // rendered below
      }
      if (token !== state.navToken) return;
    }
    renderAccountPage();
  }

  async function saveAccount(form) {
    const providerId = form.dataset.provider || '';
    const provider = state.providers.find((item) => item.id === providerId) || { id: providerId };
    const errorElement = form.querySelector('.form-error');
    setFormError(errorElement, '');

    const activeModeButton = form.querySelector('.segmented button.active');
    const mode = activeModeButton ? activeModeButton.dataset.mode : accountModes(provider)[0];
    const domainElement = form.querySelector('[name="domain"]');
    const domain = domainElement ? domainElement.value : provider.default_domain || '';
    const body = { mode, domain };

    if (mode === 'password') {
      const emailInput = form.querySelector('[name="email"]');
      const passwordInput = form.querySelector('[name="password"]');
      const email = emailInput ? emailInput.value.trim() : '';
      const password = passwordInput ? passwordInput.value : '';
      if (!email || !password) {
        setFormError(errorElement, '请填写邮箱和密码');
        if (!email && emailInput) emailInput.focus();
        else if (passwordInput) passwordInput.focus();
        return;
      }
      body.email = email;
      body.password = password;
    } else {
      const keys = credentialKeys(provider);
      if (!keys.length) {
        setFormError(errorElement, '该图书源未声明 Cookie 字段');
        return;
      }
      for (const key of keys) {
        const input = Array.from(form.querySelectorAll('input')).find((item) => item.name === key);
        const value = input ? input.value.trim() : '';
        if (!value) {
          setFormError(errorElement, `请填写 ${credentialLabel(key)}`);
          if (input) input.focus();
          return;
        }
        body[key] = value;
      }
    }

    const submitButton = form.querySelector('button[type="submit"]');
    setBusy(submitButton, true, '保存并验证中…');
    try {
      const data = await api.put(`/api/providers/${encodeURIComponent(providerId)}/account`, body);
      updateProviderAccount(providerId, data.account);
      toast('账号已保存并验证', 'success');
      renderAccountPage();
    } catch (error) {
      setFormError(errorElement, (error && error.message) || '保存失败');
    } finally {
      if (submitButton.isConnected) setBusy(submitButton, false);
    }
  }

  async function verifyAccount(providerId, button) {
    const provider = state.providers.find((item) => item.id === providerId);
    if (!provider) return;
    setBusy(button, true, '验证中…');
    try {
      const data = await api.post(`/api/providers/${encodeURIComponent(providerId)}/account/verify`, {});
      updateProviderAccount(providerId, data.account);
      toast('账号验证通过', 'success');
      renderAccountPage();
    } catch (error) {
      const card = button.closest('.provider-card');
      const errorElement = card ? card.querySelector('.form-error') : null;
      setFormError(errorElement, (error && error.message) || '验证失败');
    } finally {
      if (button.isConnected) setBusy(button, false);
    }
  }

  async function deleteAccount(providerId, button) {
    const provider = state.providers.find((item) => item.id === providerId);
    const name = provider ? provider.name || provider.id : providerId;
    const confirmed = await confirmDialog(`确定删除 ${name} 的账号凭据吗？删除后需要重新配置才能下载。`, {
      title: '删除账号凭据',
      confirmText: '删除',
    });
    if (!confirmed) return;
    setBusy(button, true, '删除中…');
    try {
      await api.del(`/api/providers/${encodeURIComponent(providerId)}/account`);
      updateProviderAccount(providerId, null);
      toast('账号凭据已删除', 'success');
      renderAccountPage();
    } catch (error) {
      const card = button.closest('.provider-card');
      setFormError(card ? card.querySelector('.form-error') : null, (error && error.message) || '删除失败');
    } finally {
      if (button.isConnected) setBusy(button, false);
    }
  }

  // ---------------------------------------------------------------------------
  // Search
  // ---------------------------------------------------------------------------
  function syncSearchProvider() {
    const ids = providerList().map((provider) => provider.id);
    if (!ids.length) return;
    if (!ids.includes(state.search.form.provider)) state.search.form.provider = ids[0];
  }

  function readSearchForm() {
    const form = document.getElementById('search-form');
    if (!form) return;
    state.search.form = {
      provider: ($('#search-provider') || {}).value || state.search.form.provider || 'zlib',
      q: (($('#search-q') || {}).value || '').trim(),
      ext: (($('#search-ext') || {}).value || ''),
      lang: (($('#search-lang') || {}).value || ''),
      year_from: (($('#search-year-from') || {}).value || '').trim(),
      year_to: (($('#search-year-to') || {}).value || '').trim(),
      order: ($('#search-order') || {}).value || 'popular',
    };
  }

  function buildSearchQuery() {
    const form = state.search.form;
    const params = new URLSearchParams();
    params.set('provider', form.provider || 'zlib');
    params.set('q', form.q);
    params.set('page', String(state.search.page || 1));
    params.set('limit', '20');
    if (form.ext) params.set('ext', form.ext);
    if (form.lang) params.set('lang', form.lang);
    if (form.year_from) params.set('year_from', form.year_from);
    if (form.year_to) params.set('year_to', form.year_to);
    params.set('order', form.order || 'popular');
    return params.toString();
  }

  function searchCardHTML(book, index) {
    const title = book.title || '未命名书籍';
    const language = `<span>${esc(languageLabel(book.language))}</span>`;
    return `<article class="book-card">
      ${coverHTML(book.cover_url, title)}
      <div class="book-main">
        <h3 class="book-title">${esc(title)}</h3>
        <div class="book-authors">${esc(authorsText(book.authors))}</div>
        <div class="book-meta">
          <span>${esc(book.year || '年份未知')}</span>
          <span>${esc(book.publisher || '出版社未知')}</span>
          <span>${esc((book.format || '格式未知').toUpperCase())}</span>
          <span>${esc(book.size || '大小未知')}</span>
          ${language}
        </div>
        <p class="book-desc">${esc(book.description || '暂无简介')}</p>
        <div class="card-actions">
          <button type="button" class="btn primary small" data-action="download-book" data-index="${index}">下载到书库</button>
        </div>
      </div>
    </article>`;
  }

  function searchResultsHTML() {
    if (state.search.error) {
      return errorHTML(state.search.error, '<button type="button" class="btn small" data-action="retry-search">重试</button>');
    }
    const data = state.search.data || {};
    const books = Array.isArray(data.books) ? data.books : [];
    if (!books.length) {
      return emptyHTML('没有找到书籍', '尝试更换关键词，或放宽格式、语言、年份筛选条件。');
    }
    const totalPages = Math.max(1, Number(data.total_pages) || 1);
    const page = Math.max(1, Number(data.page) || state.search.page || 1);
    const providerId = data.provider || state.search.form.provider;
    const provider = state.providers.find((item) => item.id === providerId);
    const providerName = provider ? provider.name || provider.id : providerId;

    return `<div class="results-meta">
        <span>来源 ${esc(providerName)} · 第 ${page} / ${totalPages} 页</span>
        <span>当前页 ${books.length} 本</span>
      </div>
      <div class="book-grid">${books.map((book, index) => searchCardHTML(book, index)).join('')}</div>
      ${totalPages > 1 ? pagebarHTML('search-page', page, totalPages) : ''}`;
  }

  function renderSearchPage() {
    if (state.tab !== 'search') return;
    syncSearchProvider();
    const form = state.search.form;
    const providers = providerList();
    const providerOptions = providers.map((provider) => `<option value="${esc(provider.id)}" ${provider.id === form.provider ? 'selected' : ''}>${esc(provider.name || provider.id)}（${esc(provider.id)}）</option>`).join('');
    const loading = state.search.status === 'loading';

    // Build each select's selected option explicitly to avoid ambiguous state.
    const extOptions = [['', '任意格式'], ['epub', 'EPUB'], ['pdf', 'PDF'], ['mobi', 'MOBI'], ['txt', 'TXT']]
      .map(([value, label]) => `<option value="${value}" ${form.ext === value ? 'selected' : ''}>${label}</option>`).join('');
    const langOptions = [['', '任意语言'], ['chinese', '中文'], ['english', '英文']]
      .map(([value, label]) => `<option value="${value}" ${form.lang === value ? 'selected' : ''}>${label}</option>`).join('');
    const orderOptions = [['popular', '热门优先'], ['bestmatch', '最佳匹配'], ['date_created', '最新收录'], ['year', '出版年份']]
      .map(([value, label]) => `<option value="${value}" ${form.order === value ? 'selected' : ''}>${label}</option>`).join('');

    let results;
    if (loading) results = loadingHTML('正在搜索…');
    else if (state.search.status === 'error') results = searchResultsHTML();
    else if (state.search.status === 'done') results = searchResultsHTML();
    else results = emptyHTML('搜索你想听的书', '输入书名、作者或关键词，选择图书源与筛选条件。');

    const providerWarning = state.providersError
      ? `<div class="notice warn"><div>图书源列表加载失败：${esc(state.providersError)}，将使用默认 zlib 配置。<button type="button" class="btn small" data-action="retry-providers">重试</button></div></div>`
      : '';

    dom.main.innerHTML = `<div class="page-head">
        <div><h1>搜索图书</h1><p class="sub">在已配置的图书源中查找电子书，然后下载到书库。</p></div>
      </div>
      ${providerWarning}
      <section class="panel search-panel">
        <form id="search-form" class="search-form">
          <div class="search-row">
            <label class="field search-field-q"><span>关键词</span><input id="search-q" type="search" value="${esc(form.q)}" placeholder="书名 / 作者 / ISBN" autocomplete="off" required></label>
            <label class="field"><span>图书源</span><select id="search-provider">${providerOptions}</select></label>
            <label class="field"><span>格式</span><select id="search-ext">${extOptions}</select></label>
            <label class="field"><span>语言</span><select id="search-lang">${langOptions}</select></label>
            <button type="submit" class="btn primary search-submit" ${loading ? 'disabled' : ''}>${loading ? '搜索中…' : '搜索'}</button>
          </div>
          <div class="search-row secondary">
            <label class="field"><span>起始年份</span><input id="search-year-from" type="number" min="0" max="2100" step="1" value="${esc(form.year_from)}" placeholder="如 2000"></label>
            <label class="field"><span>结束年份</span><input id="search-year-to" type="number" min="0" max="2100" step="1" value="${esc(form.year_to)}" placeholder="如 2024"></label>
            <label class="field"><span>排序</span><select id="search-order">${orderOptions}</select></label>
          </div>
        </form>
      </section>
      <div id="search-results">${results}</div>`;

    decorateCovers(dom.main);
  }

  async function enterSearch(token) {
    if (!state.providersLoaded) {
      dom.main.innerHTML = loadingHTML('加载图书源…');
      try {
        await ensureProviders();
      } catch (error) {
        // fall back to the default provider card
      }
      if (token !== state.navToken) return;
    }
    renderSearchPage();
  }

  async function runSearch(page = 1) {
    const form = state.search.form;
    if (!form.q) {
      toast('请输入搜索关键词', 'error');
      const input = document.getElementById('search-q');
      if (input) input.focus();
      return;
    }
    if (form.year_from && form.year_to && Number(form.year_from) > Number(form.year_to)) {
      toast('起始年份不能晚于结束年份', 'error');
      return;
    }
    state.search.page = Math.max(1, Number(page) || 1);
    state.search.error = '';
    state.search.status = 'loading';
    renderSearchPage();

    const token = ++state.search.token;
    try {
      const data = await api.get(`/api/search?${buildSearchQuery()}`);
      if (token !== state.search.token || state.tab !== 'search') return;
      state.search.data = data;
      state.search.page = Math.max(1, Number(data.page) || state.search.page);
      state.search.status = 'done';
      renderSearchPage();
    } catch (error) {
      if (token !== state.search.token || state.tab !== 'search') return;
      state.search.status = 'error';
      state.search.error = (error && error.message) || '搜索失败';
      renderSearchPage();
    }
  }

  async function downloadBook(index, button) {
    const data = state.search.data;
    const book = data && Array.isArray(data.books) ? data.books[index] : null;
    if (!book) {
      toast('书籍信息已失效，请重新搜索', 'error');
      return;
    }
    setBusy(button, true, '提交中…');
    try {
      const response = await api.post('/api/library/download', bookPayload(book));
      const job = response.job || {};
      toast(job.id != null ? `已加入下载任务 #${job.id}` : '已加入下载任务', 'success');
      state.library.loaded = false;
      state.jobs.page = 1;
      navigate('#/jobs');
    } catch (error) {
      // toast already shown by the API wrapper
    } finally {
      if (button.isConnected) setBusy(button, false);
    }
  }

  // ---------------------------------------------------------------------------
  // Library
  // ---------------------------------------------------------------------------
  function renderLibraryPage() {
    if (state.tab !== 'library') return;
    const head = `<div class="page-head">
      <div><h1>我的书库</h1><p class="sub">下载完成的电子书会保存在这里，可预览章节或转成有声书。</p></div>
      <div class="page-actions"><a class="btn" href="#/search">去搜索</a><button type="button" class="btn" data-action="refresh-library">刷新</button></div>
    </div>`;

    if (state.library.loading) {
      dom.main.innerHTML = `${head}${loadingHTML('加载书库…')}`;
      return;
    }
    if (state.library.error) {
      dom.main.innerHTML = `${head}${errorHTML(state.library.error, '<button type="button" class="btn small" data-action="retry-library">重试</button>')}`;
      return;
    }
    if (!state.library.items.length) {
      dom.main.innerHTML = `${head}${emptyHTML('书库还是空的', '先搜索并下载一本电子书，下载完成后会出现在这里。', '<a class="btn primary" href="#/search">去搜索书籍</a>')}`;
      return;
    }
    dom.main.innerHTML = `${head}<div class="library-list">${state.library.items.map(libraryCardHTML).join('')}</div>`;
    decorateCovers(dom.main);
  }

  function libraryCardHTML(book) {
    const title = book.title || '未命名书籍';
    const format = (book.format || 'EPUB').toUpperCase();
    const authors = authorsText(book.authors);
    const status = book.status || '';
    return `<article class="library-card">
      ${coverHTML(book.cover_url, title)}
      <div class="library-main">
        <h3 class="book-title">${esc(title)}</h3>
        <div class="book-authors">${esc(authors)}</div>
        <div class="book-meta">
          <span>${esc(format)}</span>
          <span>${esc(fmtBytes(book.size_bytes))}</span>
          <span>下载于 ${esc(fmtTime(book.created_at))}</span>
          <span class="badge ${tone(status)}">${esc(libraryStatusLabel(status))}</span>
        </div>
        ${book.publisher ? `<div class="kv"><span>出版社</span><span>${esc(book.publisher)}</span></div>` : ''}
        <div class="library-actions">
          <a class="btn small" href="/api/files/books/${encodeURIComponent(book.id)}" download>下载 ${esc(format)}</a>
          <button type="button" class="btn small" data-action="chapters" data-id="${esc(book.id)}">章节预览</button>
          <button type="button" class="btn small" data-action="convert-book" data-id="${esc(book.id)}">转有声书</button>
          <button type="button" class="btn small danger" data-action="delete-book" data-id="${esc(book.id)}" data-title="${esc(title)}">删除</button>
        </div>
      </div>
    </article>`;
  }

  async function enterLibrary(token) {
    state.library.loading = true;
    state.library.error = '';
    renderLibraryPage();
    try {
      const data = await api.get('/api/library');
      if (token !== state.navToken) return;
      state.library.items = Array.isArray(data.books) ? data.books : [];
      state.library.loaded = true;
      state.library.loading = false;
      renderLibraryPage();
    } catch (error) {
      if (token !== state.navToken) return;
      state.library.loading = false;
      state.library.error = (error && error.message) || '加载书库失败';
      renderLibraryPage();
    }
  }

  function findLibraryBook(id) {
    return state.library.items.find((book) => String(book.id) === String(id)) || null;
  }

  async function showChapters(bookId) {
    const book = findLibraryBook(bookId);
    const title = `章节预览${book ? ` · ${book.title || ''}` : ''}`;
    const seq = openModal({ title, body: loadingHTML('加载章节…') });
    try {
      const data = await api.get(`/api/library/${encodeURIComponent(bookId)}/chapters`);
      if (!modalIsCurrent(seq)) return;
      const chapters = Array.isArray(data.chapters) ? data.chapters : [];
      const body = chapters.length
        ? `<div class="chapter-list">${chapters.map((chapter) => {
          const index = Number(chapter.index);
          const fallbackTitle = Number.isFinite(index) ? `第 ${index + 1} 章` : '未命名章节';
          return `<div class="chapter-item">
            <div class="chapter-title"><span>${esc(chapter.title || fallbackTitle)}</span><span class="chars">${Number(chapter.chars) || 0} 字</span></div>
            <div class="chapter-preview">${esc(chapter.preview || '（暂无预览）')}</div>
          </div>`;
        }).join('')}</div>`
        : emptyHTML('暂无章节', '这本书可能还没有解析出目录。');
      openModal({ title, body, footer: '<button type="button" class="btn" data-close-modal>关闭</button>' });
    } catch (error) {
      if (!modalIsCurrent(seq)) return;
      openModal({
        title,
        body: errorHTML((error && error.message) || '加载章节失败'),
        footer: '<button type="button" class="btn" data-close-modal>关闭</button>',
      });
    }
  }

  async function deleteBook(id, button) {
    const book = findLibraryBook(id);
    const confirmed = await confirmDialog(`确定从书库删除《${book ? book.title || '未命名书籍' : '这本书'}》吗？已生成的有声书不会被自动删除。`, {
      title: '删除书籍',
      confirmText: '删除',
    });
    if (!confirmed) return;
    setBusy(button, true, '删除中…');
    try {
      await api.del(`/api/library/${encodeURIComponent(id)}`);
      state.library.items = state.library.items.filter((item) => String(item.id) !== String(id));
      toast('已从书库删除', 'success');
      renderLibraryPage();
    } catch (error) {
      // toast already shown
    } finally {
      if (button.isConnected) setBusy(button, false);
    }
  }

  // ---------------------------------------------------------------------------
  // Audiobook conversion form
  // ---------------------------------------------------------------------------
  function audiobookFormHTML({ models, chapters, health, defaultModel, maxJobChars }) {
    const localModels = models.filter((model) => modelProvider(model) === 'local');
    const healthParts = [];
    if (health && health.status) healthParts.push(health.status === 'ok' ? 'TTS 服务正常' : `TTS 服务状态：${health.status}`);
    if (health && health.version) healthParts.push(`v${health.version}`);
    if (health && health.busy) healthParts.push('忙碌');
    if (health && health.max_chars) healthParts.push(`单次上限 ${health.max_chars} 字`);
    const healthText = healthParts.length ? healthParts.join(' · ') : 'TTS 服务状态未知';

    const modelOptions = models.map((model) => `<option value="${esc(model.id)}" ${model.id === defaultModel.id ? 'selected' : ''}>${esc(model.id)} · ${esc(providerLabel(modelProvider(model)))}</option>`).join('');
    const fallbackOptions = localModels
      .filter((model) => model.id !== defaultModel.id)
      .map((model) => `<option value="${esc(model.id)}">${esc(model.id)}</option>`)
      .join('');

    const chapterList = chapters.length
      ? chapters.map((chapter) => {
        const index = Number(chapter.index);
        const title = chapter.title || (Number.isFinite(index) ? `第 ${index + 1} 章` : '未命名章节');
        return `<label class="chapter-check">
          <input type="checkbox" name="ab-chapter" value="${esc(chapter.index)}" checked>
          <span class="num">${Number.isFinite(index) ? index + 1 : '·'}</span>
          <span>${esc(title)}</span>
        </label>`;
      }).join('')
      : '<p class="hint warn">这本书没有可用章节，无法转换。</p>';

    return `<form id="audiobook-form">
      <div class="form-section">
        <h3>模型与音色</h3>
        <div class="form-grid">
          <label class="field"><span>模型</span><select id="ab-model">${modelOptions}</select><small>${esc(healthText)}</small></label>
          <label class="field"><span>音色</span><select id="ab-voice"><option value="">加载中…</option></select><small class="hint" id="ab-voice-hint">正在加载音色…</small></label>
          <label class="field"><span>语速</span>
            <span class="speed-row"><input id="ab-speed" type="range" min="0.6" max="1.6" step="0.05" value="1"><span class="speed-value" id="ab-speed-value">1.00×</span></span>
          </label>
        </div>
        <div class="sample-row">
          <button type="button" class="btn small" id="ab-sample-btn">▶ 试听当前音色</button>
          <span class="hint" id="ab-sample-hint">用固定示例文本合成当前模型/音色，首次约需几秒</span>
          <audio id="ab-sample-audio" controls preload="none" hidden></audio>
        </div>
        <label class="field"><span>语气指令（可选）</span><textarea id="ab-instruct" rows="2" maxlength="512" placeholder="例如：温柔、舒缓，适合睡前聆听"></textarea><small class="hint" id="ab-instruct-hint"></small></label>
        <label class="field"><span>失败回退模型（可选）</span><select id="ab-fallback"><option value="">不回退</option>${fallbackOptions}</select><small>仅在本地模型之间回退；主模型失败时会尝试该模型。</small></label>
      </div>
      <div class="form-section">
        <div class="chapter-toolbar">
          <h3>章节（已选 <span id="ab-chapter-count">${chapters.length}</span> / ${chapters.length}）</h3>
          <div class="row">
            <button type="button" class="btn small" data-action="select-all-chapters">全选</button>
            <button type="button" class="btn small" data-action="select-no-chapters">全不选</button>
          </div>
        </div>
        <p class="chapter-summary" id="ab-chapter-summary">正在计算字数…</p>
        <div class="chapter-check-list">${chapterList}</div>
      </div>
      <p class="form-error" id="ab-error" role="alert" hidden></p>
    </form>`;
  }

  function fallbackModelsFor(models, modelId) {
    return models.filter((model) => modelProvider(model) === 'local' && model.id !== modelId);
  }

  function mountAudiobookForm({ bookId, models, chapters, defaultModel, maxJobChars }) {
    const form = document.getElementById('audiobook-form');
    if (!form) return;
    const modelSelect = document.getElementById('ab-model');
    const voiceSelect = document.getElementById('ab-voice');
    const voiceHint = document.getElementById('ab-voice-hint');
    const speedInput = document.getElementById('ab-speed');
    const speedValue = document.getElementById('ab-speed-value');
    const instructInput = document.getElementById('ab-instruct');
    const instructHint = document.getElementById('ab-instruct-hint');
    const fallbackSelect = document.getElementById('ab-fallback');
    const errorElement = document.getElementById('ab-error');
    const submitButton = document.getElementById('ab-submit');
    const countElement = document.getElementById('ab-chapter-count');
    if (!modelSelect || !voiceSelect || !speedInput || !fallbackSelect || !submitButton) return;

    let voiceToken = 0;

    const sampleBtn = document.getElementById('ab-sample-btn');
    const sampleHint = document.getElementById('ab-sample-hint');
    const sampleAudio = document.getElementById('ab-sample-audio');
    const sampleUrls = new Map();

    function resetSample() {
      if (sampleAudio) {
        sampleAudio.pause();
        sampleAudio.hidden = true;
        sampleAudio.removeAttribute('src');
      }
      if (sampleHint) sampleHint.textContent = '用固定示例文本合成当前模型/音色，首次约需几秒';
    }

    async function previewSample() {
      const model = modelSelect.value;
      const voice = voiceSelect.value;
      if (!model) {
        toast('请先选择模型', 'error');
        return;
      }
      if (!voice) {
        toast('请先选择音色', 'error');
        return;
      }
      const key = `${model}|${voice}`;
      if (sampleBtn) setBusy(sampleBtn, true, '合成中…');
      if (sampleHint) sampleHint.textContent = '正在合成试听，请稍候…';
      try {
        let url = sampleUrls.get(key);
        if (!url) {
          const response = await fetch(`/api/tts/sample?model=${encodeURIComponent(model)}&voice=${encodeURIComponent(voice)}`, { credentials: 'same-origin' });
          if (!response.ok) {
            let message = '试听合成失败';
            try {
              const payload = await response.json();
              if (payload && payload.error && payload.error.message) message = payload.error.message;
            } catch (ignored) { /* body is not JSON */ }
            throw new Error(message);
          }
          const blob = await response.blob();
          url = URL.createObjectURL(blob);
          sampleUrls.set(key, url);
        }
        if (sampleAudio) {
          sampleAudio.src = url;
          sampleAudio.hidden = false;
          try {
            await sampleAudio.play();
          } catch (ignored) { /* browsers may block autoplay */ }
        }
        if (sampleHint) sampleHint.textContent = `正在试听：${model} / ${voice}`;
      } catch (error) {
        if (sampleHint) sampleHint.textContent = '用固定示例文本合成当前模型/音色，首次约需几秒';
        toast(error && error.message ? error.message : '试听失败', 'error');
      } finally {
        if (sampleBtn) setBusy(sampleBtn, false);
      }
    }

    const selectedModel = () => models.find((model) => model.id === modelSelect.value) || null;

    const jobCharLimit = Number(maxJobChars) || 0;
    const summaryElement = document.getElementById('ab-chapter-summary');

    function selectedChapterIndexes() {
      return $$('input[name="ab-chapter"]:checked', form)
        .map((input) => Number(input.value))
        .filter((value) => Number.isFinite(value));
    }

    function chapterChars(index) {
      const chapter = chapters.find((item) => Number(item.index) === index);
      return chapter && Number(chapter.chars) ? Number(chapter.chars) : 0;
    }

    function formatDuration(seconds) {
      if (!Number.isFinite(seconds) || seconds <= 0) return '—';
      if (seconds >= 3600) return `${(seconds / 3600).toFixed(1)} 小时`;
      return `${Math.max(1, Math.round(seconds / 60))} 分钟`;
    }

    function updateChapterSummary() {
      const selected = selectedChapterIndexes();
      const totalChars = selected.reduce((sum, index) => sum + chapterChars(index), 0);
      const over = jobCharLimit > 0 && totalChars > jobCharLimit;
      if (summaryElement) {
        if (!selected.length) {
          summaryElement.textContent = '尚未选择章节';
          summaryElement.classList.remove('warn');
        } else {
          const audioSeconds = totalChars * 0.257;
          const parts = [`已选 ${selected.length} 章 · ${totalChars.toLocaleString()} 字`];
          if (jobCharLimit > 0) parts.push(`单任务上限 ${jobCharLimit.toLocaleString()} 字`);
          parts.push(`预计音频 ≈ ${formatDuration(audioSeconds)}（Edge ≈ ${formatDuration(audioSeconds / 18)}，本地模型 ≈ ${formatDuration(audioSeconds / 2.2)}）`);
          if (over) parts.push('超过上限，请减少章节或调大 MAX_JOB_CHARS');
          summaryElement.textContent = parts.join(' · ');
          summaryElement.classList.toggle('warn', over);
        }
      }
      if (submitButton) {
        submitButton.disabled = over;
        submitButton.title = over ? '已选章节字数超过单任务上限' : '';
      }
    }

    function updateChapterCount() {
      if (countElement) {
        countElement.textContent = String($$('input[name="ab-chapter"]:checked', form).length);
      }
      updateChapterSummary();
    }

    updateChapterSummary();
    form.addEventListener('chapters-changed', updateChapterSummary);

    function syncFallbackOptions() {
      const currentId = modelSelect.value;
      const previous = fallbackSelect.value;
      const locals = fallbackModelsFor(models, currentId);
      fallbackSelect.innerHTML = `<option value="">不回退</option>${locals.map((model) => `<option value="${esc(model.id)}">${esc(model.id)}</option>`).join('')}`;
      if (previous && locals.some((model) => model.id === previous)) fallbackSelect.value = previous;
    }

    function syncInstructHint() {
      const model = selectedModel();
      const edge = modelProvider(model) === 'edge';
      if (instructHint) {
        instructHint.textContent = edge ? 'Edge 不支持语气指令，将忽略。' : '';
        instructHint.classList.toggle('warn', edge);
      }
      if (instructInput) {
        instructInput.placeholder = edge
          ? '当前模型不支持语气指令'
          : '例如：温柔、舒缓，适合睡前聆听';
      }
    }

    async function loadVoices() {
      const modelId = modelSelect.value;
      const token = ++voiceToken;
      voiceSelect.innerHTML = '<option value="">加载中…</option>';
      voiceSelect.disabled = true;
      if (voiceHint) voiceHint.textContent = '正在加载音色…';
      let voices = [];
      try {
        const data = await api.get(`/api/tts/voices?model=${encodeURIComponent(modelId)}`, { quiet: true });
        voices = Array.isArray(data.voices) ? data.voices : [];
      } catch (error) {
        const model = selectedModel();
        const raw = model && Array.isArray(model.voices) ? model.voices : [];
        voices = raw.map((voice) => (typeof voice === 'string'
          ? { id: voice, name: voice, description: '' }
          : { id: voice.id || voice.name || '', name: voice.name || voice.id || '', description: voice.description || '' }));
      }
      if (token !== voiceToken) return;
      const list = voices
        .map((voice) => ({
          id: voice.id || voice.name || '',
          name: voice.name || voice.id || '',
          description: voice.description || '',
        }))
        .filter((voice) => voice.id);
      if (!list.length) {
        voiceSelect.innerHTML = '<option value="">（无可用音色）</option>';
        voiceSelect.disabled = false;
        if (voiceHint) voiceHint.textContent = '该模型未返回音色列表';
        return;
      }
      voiceSelect.innerHTML = list.map((voice) => {
        const label = voice.description ? `${voice.name} — ${voice.description}` : voice.name;
        return `<option value="${esc(voice.id)}" title="${esc(voice.description || voice.name)}">${esc(label)}</option>`;
      }).join('');
      voiceSelect.disabled = false;
      if (voiceHint) voiceHint.textContent = `共 ${list.length} 个音色`;
    }

    modelSelect.addEventListener('change', () => {
      syncInstructHint();
      syncFallbackOptions();
      resetSample();
      loadVoices();
    });

    voiceSelect.addEventListener('change', resetSample);

    if (sampleBtn) {
      sampleBtn.addEventListener('click', previewSample);
    }

    speedInput.addEventListener('input', () => {
      const value = Number(speedInput.value);
      if (speedValue) speedValue.textContent = `${Number.isFinite(value) ? value.toFixed(2) : '1.00'}×`;
    });

    form.addEventListener('change', (event) => {
      if (event.target && event.target.name === 'ab-chapter') updateChapterCount();
    });

    submitButton.addEventListener('click', async () => {
      setFormError(errorElement, '');
      const model = modelSelect.value;
      const voice = voiceSelect.value;
      if (!model) {
        setFormError(errorElement, '请选择模型');
        return;
      }
      if (!voice) {
        setFormError(errorElement, '请选择音色');
        return;
      }
      if (!chapters.length) {
        setFormError(errorElement, '这本书没有可用章节');
        return;
      }
      const selectedChapters = $$('input[name="ab-chapter"]:checked', form)
        .map((input) => Number(input.value))
        .filter((value) => Number.isFinite(value));
      if (!selectedChapters.length) {
        setFormError(errorElement, '请至少选择一个章节');
        return;
      }
      const selectedChars = selectedChapters.reduce((sum, index) => sum + chapterChars(index), 0);
      if (jobCharLimit > 0 && selectedChars > jobCharLimit) {
        setFormError(errorElement, `已选章节共 ${selectedChars.toLocaleString()} 字，超过单任务上限 ${jobCharLimit.toLocaleString()} 字，请减少章节或调大 MAX_JOB_CHARS`);
        return;
      }
      const speed = Number(speedInput.value);
      if (!(speed >= 0.6 && speed <= 1.6)) {
        setFormError(errorElement, '语速必须在 0.6 到 1.6 之间');
        return;
      }

      setBusy(submitButton, true, '创建任务中…');
      try {
        const data = await api.post(`/api/library/${encodeURIComponent(bookId)}/audiobooks`, {
          model,
          voice,
          speed,
          instruct: instructInput ? instructInput.value.trim() : '',
          fallback_model: fallbackSelect.value || '',
          chapters: selectedChapters,
        });
        const job = data.job || {};
        closeModal();
        toast(job.id != null ? `已创建有声书任务 #${job.id}` : '已创建有声书任务', 'success');
        state.jobs.page = 1;
        navigate('#/jobs');
      } catch (error) {
        setBusy(submitButton, false);
        if (dom.modal.open) setFormError(errorElement, (error && error.message) || '提交失败');
      }
    });

    syncInstructHint();
    syncFallbackOptions();
    loadVoices();
    updateChapterCount();
  }

  async function openAudiobookForm(bookId) {
    const book = findLibraryBook(bookId);
    const title = `转有声书${book ? ` · ${book.title || ''}` : ''}`;
    const seq = openModal({ title, body: loadingHTML('加载 TTS 模型与章节…') });

    let modelsData;
    let chaptersData;
    try {
      const results = await Promise.all([
        api.get('/api/tts/models'),
        api.get(`/api/library/${encodeURIComponent(bookId)}/chapters`),
      ]);
      modelsData = results[0];
      chaptersData = results[1];
    } catch (error) {
      if (!modalIsCurrent(seq)) return;
      openModal({
        title,
        body: errorHTML((error && error.message) || '加载模型或章节失败，请确认 TTS 服务可用。'),
        footer: `<button type="button" class="btn" data-close-modal>关闭</button><button type="button" class="btn primary" data-action="retry-audiobook-form" data-book-id="${esc(bookId)}">重试</button>`,
      });
      return;
    }
    if (!modalIsCurrent(seq)) return;

    const models = Array.isArray(modelsData.models) ? modelsData.models : [];
    const chapters = Array.isArray(chaptersData.chapters) ? chaptersData.chapters : [];
    const maxJobChars = Number(modelsData.max_job_chars) || 0;
    if (!models.length) {
      openModal({
        title,
        body: emptyHTML('没有可用的 TTS 模型', 'TTS 服务没有返回模型列表，请检查服务配置后重试。'),
        footer: `<button type="button" class="btn" data-close-modal>关闭</button><button type="button" class="btn primary" data-action="retry-audiobook-form" data-book-id="${esc(bookId)}">重试</button>`,
      });
      return;
    }

    const defaultModel = models.find((model) => model.default) || models[0];
    const body = audiobookFormHTML({
      models,
      chapters,
      health: modelsData.health || {},
      defaultModel,
      maxJobChars,
    });
    openModal({
      title,
      body,
      footer: '<button type="button" class="btn ghost" data-close-modal>取消</button><button type="button" class="btn primary" id="ab-submit">开始转换</button>',
      onMount() {
        mountAudiobookForm({ bookId, models, chapters, defaultModel, maxJobChars });
      },
    });
  }

  function setAllChapters(checked) {
    $$('#audiobook-form input[name="ab-chapter"]').forEach((input) => {
      input.checked = checked;
    });
    const count = $('#ab-chapter-count');
    if (count) count.textContent = checked ? String($$('#audiobook-form input[name="ab-chapter"]').length) : '0';
    const form = document.getElementById('audiobook-form');
    if (form) form.dispatchEvent(new Event('chapters-changed'));
  }

  // ---------------------------------------------------------------------------
  // Jobs
  // ---------------------------------------------------------------------------
  function startJobPolling() {
    stopJobPolling();
    if (state.tab !== 'jobs') return;
    state.jobs.timer = window.setInterval(() => {
      if (state.tab !== 'jobs') {
        stopJobPolling();
        return;
      }
      if (document.hidden) return;
      loadJobs({ page: state.jobs.page, silent: true });
    }, 1500);
  }

  function stopJobPolling() {
    if (state.jobs.timer) {
      window.clearInterval(state.jobs.timer);
      state.jobs.timer = null;
    }
  }

  function jobCardHTML(job) {
    const status = job.status || 'unknown';
    const progress = Math.max(0, Math.min(100, Number(job.progress) || 0));
    const kind = job.kind === 'audiobook' ? '有声书' : '下载';
    const actions = [];
    if (status === 'queued' || status === 'running') {
      actions.push(`<button type="button" class="btn small danger" data-action="cancel-job" data-id="${esc(job.id)}">取消任务</button>`);
    }
    if (status === 'done' && job.kind === 'audiobook' && job.result && job.result.audiobook_id != null) {
      actions.push(`<a class="btn small primary" href="#/audiobooks/${encodeURIComponent(job.result.audiobook_id)}">去播放</a>`);
    }
    if (status === 'done' && job.kind === 'download') {
      actions.push('<a class="btn small" href="#/library">查看书库</a>');
    }
    if (status === 'failed' && job.kind === 'audiobook' && job.ref_id != null) {
      actions.push(`<button type="button" class="btn small" data-action="convert-book" data-id="${esc(job.ref_id)}">重新转换</button>`);
    }
    const progressClass = status === 'failed' ? 'bad' : status === 'queued' ? 'idle' : '';
    return `<article class="job-card">
      <div class="job-top">
        <span class="badge ${job.kind === 'audiobook' ? 'info' : 'muted'}">${kind}</span>
        <span class="job-title">任务 #${esc(job.id)}</span>
        <span class="badge ${tone(status)}">${esc(jobStatusLabel(status))}</span>
      </div>
      <div class="job-progress-row">
        <div class="progress ${progressClass}" role="progressbar" aria-label="任务进度" aria-valuemin="0" aria-valuemax="100" aria-valuenow="${progress}"><span style="width:${progress}%"></span></div>
        <span class="job-percent">${progress}%</span>
      </div>
      <div class="job-meta">
        <span>创建 ${esc(fmtTime(job.created_at))}</span>
        ${job.started_at ? `<span>开始 ${esc(fmtTime(job.started_at))}</span>` : ''}
        ${job.finished_at ? `<span>结束 ${esc(fmtTime(job.finished_at))}</span>` : ''}
      </div>
      ${job.error ? `<div class="job-error">${esc(job.error)}</div>` : ''}
      ${actions.length ? `<div class="job-actions">${actions.join('')}</div>` : ''}
    </article>`;
  }

  function renderJobsPage() {
    if (state.tab !== 'jobs') return;
    const total = state.jobs.total || state.jobs.items.length;
    const totalPages = Math.max(1, Math.ceil(total / 20));
    const head = `<div class="page-head">
      <div><h1>任务</h1><p class="sub">下载与有声书转换任务，运行中每 1.5 秒自动刷新。</p></div>
      <div class="page-actions">
        <span class="auto-refresh"><span class="dot"></span>${state.jobs.lastUpdated ? `更新于 ${esc(fmtTime(state.jobs.lastUpdated))}` : '自动刷新中'}</span>
        <button type="button" class="btn" data-action="refresh-jobs">刷新</button>
      </div>
    </div>`;

    if (state.jobs.loading && !state.jobs.items.length) {
      dom.main.innerHTML = `${head}${loadingHTML('加载任务…')}`;
      return;
    }
    if (state.jobs.error && !state.jobs.items.length) {
      dom.main.innerHTML = `${head}${errorHTML(state.jobs.error, '<button type="button" class="btn small" data-action="retry-jobs">重试</button>')}`;
      return;
    }
    if (!state.jobs.items.length) {
      dom.main.innerHTML = `${head}${emptyHTML('还没有任务', '从搜索结果下载书籍，或把书库中的书转换为有声书，任务会出现在这里。', '<a class="btn primary" href="#/search">去搜索书籍</a>')}`;
      return;
    }

    const warning = state.jobs.error
      ? `<div class="notice warn"><div>刷新失败：${esc(state.jobs.error)}，当前展示上次结果。</div></div>`
      : '';
    dom.main.innerHTML = `${head}${warning}<div class="jobs-list">${state.jobs.items.map(jobCardHTML).join('')}</div>${totalPages > 1 ? pagebarHTML('jobs-page', state.jobs.page, totalPages) : ''}`;
  }

  async function loadJobs({ page = state.jobs.page, token = null, silent = false } = {}) {
    if (state.jobs.fetching) return;
    state.jobs.fetching = true;
    if (!silent) {
      state.jobs.loading = true;
      state.jobs.error = '';
      renderJobsPage();
    }
    try {
      const safePage = Math.max(1, Number(page) || 1);
      const offset = (safePage - 1) * 20;
      const data = await api.get(`/api/jobs?limit=20&offset=${offset}`, { quiet: silent });
      if (token !== null && token !== state.navToken) return;
      state.jobs.items = Array.isArray(data.jobs) ? data.jobs : [];
      state.jobs.total = Number(data.total) || 0;
      state.jobs.page = safePage;
      state.jobs.loading = false;
      state.jobs.error = '';
      state.jobs.lastUpdated = new Date();
      renderJobsPage();
    } catch (error) {
      if (token !== null && token !== state.navToken) return;
      if (!silent) {
        state.jobs.loading = false;
        state.jobs.error = (error && error.message) || '加载任务失败';
        renderJobsPage();
      }
    } finally {
      state.jobs.fetching = false;
    }
  }

  async function enterJobs(token) {
    state.jobs.page = Math.max(1, state.jobs.page || 1);
    state.jobs.loading = true;
    state.jobs.error = '';
    renderJobsPage();
    await loadJobs({ page: state.jobs.page, token });
    if (token !== state.navToken) return;
    startJobPolling();
  }

  async function cancelJob(id, button) {
    setBusy(button, true, '取消中…');
    try {
      const data = await api.post(`/api/jobs/${encodeURIComponent(id)}/cancel`, {});
      if (data.job) {
        const index = state.jobs.items.findIndex((job) => String(job.id) === String(id));
        if (index >= 0) state.jobs.items[index] = data.job;
      }
      toast(`任务 #${id} 已请求取消`, 'info');
      renderJobsPage();
      loadJobs({ page: state.jobs.page, silent: true });
    } catch (error) {
      // toast already shown
    } finally {
      if (button.isConnected) setBusy(button, false);
    }
  }

  // ---------------------------------------------------------------------------
  // Audiobooks
  // ---------------------------------------------------------------------------
  function renderAudiobooksPage() {
    if (state.tab !== 'audiobooks') return;
    const head = `<div class="page-head">
      <div><h1>有声书</h1><p class="sub">转换完成的有声书可以播放、同步查看字幕并下载文件。</p></div>
      <div class="page-actions"><button type="button" class="btn" data-action="refresh-audiobooks">刷新</button></div>
    </div>`;

    if (state.audiobooks.loading) {
      dom.main.innerHTML = `${head}${loadingHTML('加载有声书…')}`;
      return;
    }
    if (state.audiobooks.error) {
      dom.main.innerHTML = `${head}${errorHTML(state.audiobooks.error, '<button type="button" class="btn small" data-action="retry-audiobooks">重试</button>')}`;
      return;
    }
    if (!state.audiobooks.items.length) {
      dom.main.innerHTML = `${head}${emptyHTML('还没有有声书', '在书库中选择一本书，点击「转有声书」即可生成。', '<a class="btn primary" href="#/library">去书库</a>')}`;
      return;
    }
    dom.main.innerHTML = `${head}<div class="audio-grid">${state.audiobooks.items.map(audiobookCardHTML).join('')}</div>`;
  }

  function audiobookCardHTML(audiobook) {
    const title = audiobook.book_title || `有声书 #${audiobook.id}`;
    const status = audiobook.status || 'unknown';
    const progress = Math.max(0, Math.min(100, Number(audiobook.progress) || 0));
    return `<article class="audio-card">
      <div class="audio-icon" aria-hidden="true">♫</div>
      <div class="audio-main">
        <div class="audio-title">${esc(title)}</div>
        <div class="book-meta">
          <span>${esc(audiobook.model || '模型未知')}</span>
          <span>${esc(audiobook.voice || '音色未知')}</span>
          <span>${esc(fmtSpeed(audiobook.speed))}</span>
          <span class="badge ${tone(status)}">${esc(jobStatusLabel(status))}</span>
        </div>
        <div class="book-meta">
          <span>${audiobook.duration_s ? esc(fmtDuration(audiobook.duration_s)) : '时长未知'}</span>
          <span>${esc(fmtBytes(audiobook.size_bytes))}</span>
          <span>${esc(fmtTime(audiobook.created_at))}</span>
        </div>
        ${status === 'running' || status === 'queued' ? `<div class="progress small" role="progressbar" aria-label="生成进度" aria-valuemin="0" aria-valuemax="100" aria-valuenow="${progress}"><span style="width:${progress}%"></span></div>` : ''}
        ${audiobook.error ? `<div class="job-error">${esc(audiobook.error)}</div>` : ''}
        <div class="card-actions"><a class="btn small primary" href="#/audiobooks/${encodeURIComponent(audiobook.id)}">查看 / 播放</a></div>
      </div>
    </article>`;
  }

  async function enterAudiobooks(token) {
    state.audiobooks.loading = true;
    state.audiobooks.error = '';
    renderAudiobooksPage();
    try {
      const data = await api.get('/api/audiobooks');
      if (token !== state.navToken) return;
      state.audiobooks.items = Array.isArray(data.audiobooks) ? data.audiobooks : [];
      state.audiobooks.loaded = true;
      state.audiobooks.loading = false;
      renderAudiobooksPage();
    } catch (error) {
      if (token !== state.navToken) return;
      state.audiobooks.loading = false;
      state.audiobooks.error = (error && error.message) || '加载有声书失败';
      renderAudiobooksPage();
    }
  }

  function metaItem(label, value) {
    return `<div class="meta-item"><span>${esc(label)}</span><strong>${esc(value ?? '—')}</strong></div>`;
  }

  function renderAudiobookDetailPage() {
    if (state.tab !== 'audiobooks') return;
    const detail = state.audiobookDetail;
    const back = '<div class="row"><a class="btn ghost small" href="#/audiobooks">← 返回有声书列表</a></div>';

    if (!detail || detail.loading) {
      dom.main.innerHTML = `${back}${loadingHTML('加载有声书详情…')}`;
      return;
    }
    if (detail.error || !detail.audiobook) {
      dom.main.innerHTML = `${back}${errorHTML(detail.error || '有声书不存在', '<button type="button" class="btn small" data-action="retry-audiobook-detail">重试</button>')}`;
      return;
    }

    const audiobook = detail.audiobook;
    const title = audiobook.book_title || `有声书 #${audiobook.id}`;
    const status = audiobook.status || 'unknown';
    const progress = Math.max(0, Math.min(100, Number(audiobook.progress) || 0));
    const done = status === 'done';

    const playerSection = done
      ? `<div class="player-wrap">
          ${audiobook.audio_url || audiobook.chapters_url
            ? `<audio id="ab-audio" controls preload="metadata" aria-label="有声书播放器"></audio><p class="player-hint" id="player-error" hidden></p><div id="chapter-player" class="chapter-player"></div>`
            : '<div class="notice warn"><div>音频文件暂不可用。</div></div>'}
          <div>
            <div class="subtitle-label">字幕同步</div>
            <div class="subtitle-now" id="subtitle-now" aria-live="polite"></div>
          </div>
          <div id="transcript-panel"></div>
          <div class="player-links">
            ${audiobook.audio_url ? `<a class="btn small" href="${esc(audiobook.audio_url)}" download>下载整本音频</a>` : '<span class="hint">整本音频已按章节保存，可在章节列表中逐章下载。</span>'}
            ${audiobook.srt_url ? `<a class="btn small" href="${esc(audiobook.srt_url)}" download>下载整本字幕（SRT）</a>` : '<span class="hint">没有字幕文件</span>'}
          </div>
        </div>`
      : `<div class="player-wrap">
          <div class="quota-head"><span>生成进度</span><strong>${progress}%</strong></div>
          <div class="progress ${status === 'failed' ? 'bad' : ''}" role="progressbar" aria-label="生成进度" aria-valuemin="0" aria-valuemax="100" aria-valuenow="${progress}"><span style="width:${progress}%"></span></div>
          <p class="hint">${status === 'failed' ? '生成失败，请查看任务详情。' : '任务完成后即可在这里播放。'} <a href="#/jobs">去任务页</a></p>
        </div>`;

    dom.main.innerHTML = `${back}
      <div class="detail">
        <section class="detail-card">
          <div class="detail-head">
            <div>
              <h1 class="detail-title">${esc(title)}</h1>
              <div class="row" style="margin-top:8px">
                <span class="badge ${tone(status)}">${esc(jobStatusLabel(status))}</span>
                <span class="muted small">有声书 #${esc(audiobook.id)}</span>
              </div>
            </div>
            <div class="page-actions">
              <button type="button" class="btn danger small" data-action="delete-audiobook" data-id="${esc(audiobook.id)}" data-title="${esc(title)}">删除有声书</button>
            </div>
          </div>
          ${audiobook.error ? `<div class="notice bad" style="margin-top:16px"><div>${esc(audiobook.error)}</div></div>` : ''}
          <div class="meta-grid">
            ${metaItem('模型', audiobook.model || '—')}
            ${metaItem('音色', audiobook.voice || '—')}
            ${metaItem('语速', fmtSpeed(audiobook.speed))}
            ${metaItem('格式', (audiobook.format || '—').toUpperCase())}
            ${metaItem('时长', audiobook.duration_s ? fmtDuration(audiobook.duration_s) : '—')}
            ${metaItem('大小', fmtBytes(audiobook.size_bytes))}
            ${metaItem('分段', audiobook.segments != null ? String(audiobook.segments) : '—')}
            ${metaItem('字数', audiobook.chars != null ? String(audiobook.chars) : '—')}
            ${metaItem('创建时间', fmtTime(audiobook.created_at))}
            ${metaItem('完成时间', audiobook.finished_at ? fmtTime(audiobook.finished_at) : '—')}
          </div>
          ${playerSection}
        </section>
      </div>`;

    setupAudiobookPlayer(state.navToken);
  }

  async function enterAudiobookDetail(id, token) {
    state.audiobookDetail = { id, loading: true, error: '', audiobook: null };
    renderAudiobookDetailPage();
    try {
      const data = await api.get(`/api/audiobooks/${encodeURIComponent(id)}`);
      if (token !== state.navToken) return;
      state.audiobookDetail = { id, loading: false, error: '', audiobook: data.audiobook || null };
      renderAudiobookDetailPage();
    } catch (error) {
      if (token !== state.navToken) return;
      state.audiobookDetail = { id, loading: false, error: (error && error.message) || '加载有声书详情失败', audiobook: null };
      renderAudiobookDetailPage();
    }
  }

  function parseSRT(text) {
    const clean = String(text || '').replace(/^\uFEFF/, '').replace(/\r\n?/g, '\n');
    const blocks = clean.split(/\n\s*\n/);
    const timingPattern = /^(\d{1,3}):(\d{2}):(\d{2})[,.](\d{1,3})\s*-->\s*(\d{1,3}):(\d{2}):(\d{2})[,.](\d{1,3})/;
    const cues = [];
    for (const block of blocks) {
      const lines = block.split('\n');
      const timingIndex = lines.findIndex((line) => timingPattern.test(line.trim()));
      if (timingIndex < 0) continue;
      const match = lines[timingIndex].trim().match(timingPattern);
      if (!match) continue;
      const start = Number(match[1]) * 3600 + Number(match[2]) * 60 + Number(match[3]) + Number(match[4].padEnd(3, '0')) / 1000;
      const end = Number(match[5]) * 3600 + Number(match[6]) * 60 + Number(match[7]) + Number(match[8].padEnd(3, '0')) / 1000;
      const cueText = lines.slice(timingIndex + 1).join('\n').trim();
      if (!cueText) continue;
      cues.push({ start, end, text: cueText });
    }
    cues.sort((a, b) => a.start - b.start);
    return cues;
  }

  function findCueIndex(cues, time) {
    if (!cues.length) return -1;
    let low = 0;
    let high = cues.length - 1;
    let result = -1;
    while (low <= high) {
      const middle = (low + high) >> 1;
      if (cues[middle].start <= time) {
        result = middle;
        low = middle + 1;
      } else {
        high = middle - 1;
      }
    }
    if (result < 0) return -1;
    if (time <= cues[result].end) return result;
    return -1;
  }

  function setupAudiobookPlayer(token) {
    const audio = document.getElementById('ab-audio');
    if (!audio) return;
    state.audioEl = audio;
    audio.addEventListener('error', () => {
      const hint = document.getElementById('player-error');
      if (hint) {
        hint.hidden = false;
        hint.textContent = '音频加载失败，文件可能尚未生成或已被删除。';
      }
    });

    const detail = state.audiobookDetail;
    const audiobook = detail && detail.audiobook;
    const now = document.getElementById('subtitle-now');
    const panel = document.getElementById('transcript-panel');
    if (!audiobook || !now || !panel) return;

    let cues = [];
    let items = [];
    let activeIndex = -2;
    let currentChunk = -1;
    let chunks = [];

    function sync() {
      if (token !== state.navToken) return;
      const index = findCueIndex(cues, audio.currentTime || 0);
      if (index === activeIndex) return;
      if (activeIndex >= 0 && items[activeIndex]) items[activeIndex].classList.remove('active');
      activeIndex = index;
      if (index >= 0 && items[index]) {
        items[index].classList.add('active');
        now.textContent = cues[index].text;
        const list = items[index].parentElement;
        if (list) {
          const itemTop = items[index].offsetTop;
          const itemBottom = itemTop + items[index].offsetHeight;
          if (itemTop < list.scrollTop || itemBottom > list.scrollTop + list.clientHeight) {
            list.scrollTop = Math.max(0, itemTop - list.clientHeight / 2);
          }
        }
      } else {
        now.textContent = '';
      }
    }

    audio.addEventListener('timeupdate', sync);
    audio.addEventListener('seeked', sync);
    panel.addEventListener('click', (event) => {
      const item = event.target.closest('li[data-start]');
      if (!item) return;
      const start = Number(item.dataset.start);
      if (!Number.isFinite(start)) return;
      audio.currentTime = start + 0.01;
      const promise = audio.play();
      if (promise && promise.catch) promise.catch(() => {});
    });

    async function setSubtitles(url) {
      cues = [];
      items = [];
      activeIndex = -2;
      now.textContent = '';
      if (!url) {
        panel.innerHTML = '<p class="hint">本段没有字幕文件。</p>';
        return;
      }
      panel.innerHTML = '<div class="inline-loading"><span class="spinner small"></span>加载字幕…</div>';
      let text;
      try {
        text = await api.text(url, { quiet: true });
      } catch (error) {
        if (token !== state.navToken || !panel.isConnected) return;
        panel.innerHTML = `<p class="hint">字幕加载失败：${esc((error && error.message) || '未知错误')}</p>`;
        now.textContent = '字幕加载失败';
        return;
      }
      if (token !== state.navToken || !panel.isConnected) return;
      cues = parseSRT(text);
      if (!cues.length) {
        panel.innerHTML = '<p class="hint">字幕文件为空或格式无法识别。</p>';
        now.textContent = '没有可用字幕';
        return;
      }
      panel.innerHTML = `<ol class="transcript" id="transcript">${cues.map((cue, index) => `<li data-start="${cue.start}" data-index="${index}"><span class="cue-time">${esc(fmtClock(cue.start))}</span><span>${esc(cue.text)}</span></li>`).join('')}</ol>`;
      items = Array.from(panel.querySelectorAll('li'));
      activeIndex = -2;
      sync();
    }

    function renderChapterList() {
      const box = document.getElementById('chapter-player');
      if (!box) return;
      if (!chunks.length) {
        box.innerHTML = '';
        return;
      }
      box.innerHTML = `<div class="chapter-player-head"><span>章节播放</span><span class="hint">已按章节切块（单块 ≤30 分钟），点播即载入，避免一次加载整本大文件。</span></div>
        <ol class="chapter-list">${chunks.map((chunk, index) => `<li data-index="${index}" class="${index === currentChunk ? 'active' : ''}">
          <button type="button" class="chapter-play" data-index="${index}" aria-label="播放 ${esc(chunk.title)}">▶</button>
          <span class="chapter-title">${esc(chunk.title)}</span>
          <span class="chapter-dur">${esc(fmtDuration(chunk.duration_s))}</span>
          <a class="chapter-dl" href="${esc(chunk.audio_url)}" download>下载</a>
        </li>`).join('')}</ol>`;
      box.querySelectorAll('button.chapter-play').forEach((button) => {
        button.addEventListener('click', () => selectChunk(Number(button.dataset.index), true));
      });
    }

    function selectChunk(index, autoplay) {
      if (index < 0 || index >= chunks.length) return;
      currentChunk = index;
      const chunk = chunks[index];
      audio.src = chunk.audio_url;
      setSubtitles(chunk.srt_url);
      document.querySelectorAll('#chapter-player li').forEach((li, i) => {
        li.classList.toggle('active', i === index);
      });
      if (autoplay) {
        const promise = audio.play();
        if (promise && promise.catch) promise.catch(() => {});
      }
    }

    audio.addEventListener('ended', () => {
      if (currentChunk + 1 < chunks.length) selectChunk(currentChunk + 1, true);
    });

    async function loadChapters() {
      if (!audiobook.chapters_url) {
        audio.src = audiobook.audio_url || '';
        setSubtitles(audiobook.srt_url);
        return;
      }
      try {
        const data = await api.get(audiobook.chapters_url, { quiet: true });
        if (token !== state.navToken) return;
        chunks = Array.isArray(data.chunks) ? data.chunks : [];
      } catch (error) {
        chunks = [];
      }
      if (!chunks.length) {
        audio.src = audiobook.audio_url || '';
        setSubtitles(audiobook.srt_url);
        return;
      }
      renderChapterList();
      selectChunk(0, false);
    }

    loadChapters();
  }

  async function deleteAudiobook(id, button) {
    const detail = state.audiobookDetail;
    const title = detail && detail.audiobook ? detail.audiobook.book_title || `#${id}` : button.dataset.title || `#${id}`;
    const confirmed = await confirmDialog(`确定删除有声书《${title}》吗？音频与字幕文件会一并删除。`, {
      title: '删除有声书',
      confirmText: '删除',
    });
    if (!confirmed) return;
    setBusy(button, true, '删除中…');
    try {
      await api.del(`/api/audiobooks/${encodeURIComponent(id)}`);
      toast('有声书已删除', 'success');
      state.audiobookDetail = null;
      state.audiobooks.loaded = false;
      navigate('#/audiobooks');
    } catch (error) {
      if (button.isConnected) setBusy(button, false);
    }
  }

  function stopAudio() {
    if (!state.audioEl) return;
    try {
      state.audioEl.pause();
    } catch (err) {
      // ignore
    }
    state.audioEl = null;
  }

  // ---------------------------------------------------------------------------
  // Global events
  // ---------------------------------------------------------------------------
  function actionErrorMessage(error) {
    if (error instanceof ApiError && error.status === 401) return '';
    return (error && error.message) || '操作失败';
  }

  function runAction(promise) {
    Promise.resolve(promise).catch((error) => {
      const message = actionErrorMessage(error);
      if (message) toast(message, 'error');
    });
  }

  function onGlobalClick(event) {
    const closeButton = event.target.closest('[data-close-modal]');
    if (closeButton) {
      closeModal();
      return;
    }

    const trigger = event.target.closest('[data-action]');
    if (!trigger || trigger.disabled) return;
    const action = trigger.dataset.action;

    switch (action) {
      case 'logout':
        runAction(logout(trigger));
        break;
      case 'account-mode': {
        const card = trigger.closest('.provider-card');
        if (!card) break;
        card.querySelectorAll('[data-action="account-mode"]').forEach((button) => {
          button.classList.toggle('active', button === trigger);
        });
        card.querySelectorAll('[data-account-fields]').forEach((group) => {
          group.hidden = group.dataset.accountFields !== trigger.dataset.mode;
        });
        setFormError(card.querySelector('.form-error'), '');
        break;
      }
      case 'verify-account':
        runAction(verifyAccount(trigger.dataset.providerId, trigger));
        break;
      case 'delete-account':
        runAction(deleteAccount(trigger.dataset.providerId, trigger));
        break;
      case 'refresh-account':
      case 'retry-account':
        runAction(router());
        break;
      case 'retry-providers':
        runAction((async () => {
          await ensureProviders(true);
          if (state.tab === 'search') renderSearchPage();
          else if (state.tab === 'account') renderAccountPage();
        })());
        break;
      case 'search-page': {
        readSearchForm();
        runAction(runSearch(Number(trigger.dataset.page) || 1));
        break;
      }
      case 'retry-search':
        runAction(runSearch(state.search.page || 1));
        break;
      case 'download-book':
        runAction(downloadBook(Number(trigger.dataset.index), trigger));
        break;
      case 'refresh-library':
      case 'retry-library':
        runAction(router());
        break;
      case 'chapters':
        runAction(showChapters(trigger.dataset.id));
        break;
      case 'delete-book':
        runAction(deleteBook(trigger.dataset.id, trigger));
        break;
      case 'convert-book':
        runAction(openAudiobookForm(trigger.dataset.id));
        break;
      case 'retry-audiobook-form':
        runAction(openAudiobookForm(trigger.dataset.bookId));
        break;
      case 'select-all-chapters':
        setAllChapters(true);
        break;
      case 'select-no-chapters':
        setAllChapters(false);
        break;
      case 'refresh-jobs':
      case 'retry-jobs':
        runAction(router());
        break;
      case 'jobs-page':
        runAction(loadJobs({ page: Number(trigger.dataset.page) || 1, token: state.navToken }));
        break;
      case 'cancel-job':
        runAction(cancelJob(trigger.dataset.id, trigger));
        break;
      case 'refresh-audiobooks':
      case 'retry-audiobooks':
        runAction(router());
        break;
      case 'retry-audiobook-detail':
        runAction(router());
        break;
      case 'delete-audiobook':
        runAction(deleteAudiobook(trigger.dataset.id, trigger));
        break;
      default:
        break;
    }
  }

  function onGlobalSubmit(event) {
    const form = event.target;
    if (!form) return;
    if (form.id === 'search-form') {
      event.preventDefault();
      readSearchForm();
      runSearch(1);
    } else if (form.dataset && form.dataset.form === 'account') {
      event.preventDefault();
      runAction(saveAccount(form));
    } else if (form.id === 'audiobook-form') {
      event.preventDefault();
      const submit = document.getElementById('ab-submit');
      if (submit) submit.click();
    }
  }

  function bindEvents() {
    dom.authForm.addEventListener('submit', submitAuth);
    dom.authTabs.addEventListener('click', (event) => {
      const button = event.target.closest('[data-auth-mode]');
      if (!button) return;
      setAuthMode(button.dataset.authMode);
    });
    document.addEventListener('click', onGlobalClick);
    document.addEventListener('submit', onGlobalSubmit);
    window.addEventListener('hashchange', () => {
      if (state.user) router();
    });
    dom.modal.addEventListener('click', (event) => {
      if (event.target === dom.modal) closeModal();
    });
    dom.modal.addEventListener('close', handleModalClosed);
    // Capture phase catches resource errors even when they fire before a
    // card's own listener is attached (fast 404s from cached responses).
    document.addEventListener('error', (event) => {
      const target = event.target;
      if (target && typeof target.matches === 'function' && target.matches('img[data-cover]')) {
        const wrap = target.closest('.cover');
        if (wrap) wrap.classList.add('cover-fallback');
      }
    }, true);
    document.addEventListener('visibilitychange', () => {
      if (!document.hidden && state.tab === 'jobs' && state.user) {
        loadJobs({ page: state.jobs.page, silent: true });
      }
    });
  }

  // ---------------------------------------------------------------------------
  // Start
  // ---------------------------------------------------------------------------
  boot();
})();
