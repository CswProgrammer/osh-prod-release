(() => {
  'use strict';

  const $ = (id) => document.getElementById(id);
  const state = {
    health: null, current: null, currentBlue: null, pollTimer: null, blueDeployPollTimer: null,
    busy: false, page: 'deploy', activeDeploy: null, activePollTimer: null, traffic: null,
    trafficLoading: false, blueActive: null, bluePollTimer: null,
    user: null, token: sessionStorage.getItem('osh_token') || '',
  };

  function isAdmin() {
    return !!state.user?.is_admin;
  }

  function isBoss() {
    return !!state.user?.is_boss;
  }

  function authHeaders() {
    const h = { 'Content-Type': 'application/json' };
    if (state.token) h.Authorization = `Bearer ${state.token}`;
    return h;
  }

  function showApp(loggedIn) {
    const login = $('loginScreen');
    const app = $('appLayout');
    if (!login || !app) return;
    if (loggedIn) {
      login.hidden = true;
      login.setAttribute('hidden', '');
      app.hidden = false;
      app.removeAttribute('hidden');
    } else {
      login.hidden = false;
      login.removeAttribute('hidden');
      app.hidden = true;
      app.setAttribute('hidden', '');
    }
  }

  function renderUserBadge() {
    const badge = $('userBadge');
    const logout = $('btnLogout');
    if (!badge) return;
    if (!state.user) {
      badge.hidden = true;
      if (logout) logout.hidden = true;
      return;
    }
    badge.hidden = false;
    badge.textContent = isAdmin() ? `${state.user.display_name || state.user.username} · 管理员` : (state.user.display_name || state.user.username);
    badge.className = `badge user-badge${isAdmin() ? ' admin' : ''}`;
    if (logout) logout.hidden = false;
    const author = $('author');
    const blueAuthor = $('blueAuthor');
    if (author) {
      author.value = state.user.username;
      author.readOnly = true;
    }
    if (blueAuthor) {
      blueAuthor.value = state.user.username;
      blueAuthor.readOnly = true;
    }
  }

  async function login() {
    const username = $('loginUser').value.trim();
    const password = $('loginPass').value;
    const errEl = $('loginError');
    errEl.hidden = true;
    try {
      const res = await fetch('/api/auth/login', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ username, password }),
      });
      const data = await res.json().catch(() => ({}));
      if (!res.ok) throw new Error(data.error || '登录失败');
      state.token = data.token;
      state.user = data.user;
      sessionStorage.setItem('osh_token', state.token);
      showApp(true);
      renderUserBadge();
      await bootApp();
    } catch (err) {
      errEl.hidden = false;
      errEl.textContent = err.message || String(err);
    }
  }

  async function logout() {
    try {
      await fetch('/api/auth/logout', { method: 'POST', headers: authHeaders() });
    } catch { /* ignore */ }
    state.token = '';
    state.user = null;
    sessionStorage.removeItem('osh_token');
    showApp(false);
  }

  async function restoreSession() {
    if (!state.token) {
      showApp(false);
      return false;
    }
    try {
      const res = await fetch('/api/auth/me', { headers: authHeaders() });
      const data = await res.json().catch(() => ({}));
      if (!res.ok) throw new Error(data.error || 'session expired');
      state.user = data;
      showApp(true);
      renderUserBadge();
      return true;
    } catch {
      state.token = '';
      sessionStorage.removeItem('osh_token');
      showApp(false);
      return false;
    }
  }

  async function loadDeploySnapshotsFor(target, listId, btnId) {
    const el = $(listId);
    const btn = $(btnId);
    if (!el) return;
    if (!isAdmin()) {
      el.innerHTML = '<div class="empty">仅管理员可查看回滚</div>';
      if (btn) btn.hidden = true;
      return;
    }
    try {
      const list = await api(`/api/deploy/snapshots?target=${target}`);
      if (!list.length) {
        el.innerHTML = '<div class="empty">暂无部署快照</div>';
        if (btn) btn.hidden = true;
        return;
      }
      el.innerHTML = list.slice(0, 5).map((s, i) => `
        <div class="rollback-item">
          <strong>${i === 0 ? '当前' : '历史'} · ${escapeHtml(s.title)}</strong>
          <span class="meta">${new Date(s.created_at).toLocaleString()} · ${escapeHtml(s.backend_git_ref)} / ${escapeHtml(s.frontend_git_ref)}</span>
        </div>`).join('');
      if (btn) btn.hidden = list.length < 2;
    } catch {
      el.innerHTML = '<div class="empty">加载失败</div>';
      if (btn) btn.hidden = true;
    }
  }

  async function loadDeploySnapshots() {
    await loadDeploySnapshotsFor('green', 'rollbackList', 'btnRollbackGreen');
    await loadDeploySnapshotsFor('blue', 'blueRollbackList', 'btnRollbackBlue');
  }

  async function rollbackDeploy(target) {
    if (!isAdmin()) {
      toast('仅管理员可执行版本回滚', 'error');
      return;
    }
    const label = target === 'blue' ? '蓝环境' : '绿环境';
    if (!confirm(`确认将${label}回滚到上一成功部署版本？\n\n将通过 GHA 重新部署上一版本的 git ref。`)) return;
    state.busy = true;
    $('loading').classList.add('show');
    try {
      const snap = await api('/api/deploy/rollback', {
        method: 'POST',
        body: JSON.stringify({ target, to_previous: true, actor: state.user.username, reason: 'manual rollback' }),
      });
      toast(`回滚完成：${snap.title || snap.id}`);
      await loadDeploySnapshots();
    } catch (err) {
      toast(err.message || String(err), 'error');
    } finally {
      state.busy = false;
      $('loading').classList.remove('show');
    }
  }

  const PAGE_GROUPS = {
    deploy: 'green',
    sql: 'green',
    traffic: null,
    'deploy-blue': 'blue',
    'sql-blue': 'blue',
    'sync-blue': 'blue',
  };

  const PAGES = {
    deploy: {
      title: '部署绿环境',
      subtitle: '4 步向导 · GitHub Actions · 不影响蓝环境生产',
    },
    sql: {
      title: '更新绿环境数据库',
      subtitle: '自定义 SQL · 仅 osh-g-mysql · 与部署独立',
    },
    traffic: {
      title: '生产切流',
      subtitle: '蓝 ↔ 绿 · 149 :80 入口切换 · 与部署独立',
    },
    'deploy-blue': {
      title: '部署蓝环境',
      subtitle: '4 步向导 · GHA slot=blue · 验收 :58080',
    },
    'sql-blue': {
      title: '更新蓝环境数据库',
      subtitle: '自定义 SQL · 仅 osh-mysql · 增量更新（非全量同步）',
    },
    'sync-blue': {
      title: '全量同步蓝环境数据库',
      subtitle: '绿 → 蓝 · MySQL/Nacos/ES/Redis · 覆盖蓝库中间件',
    },
  };

  const STEPS = {
    1: {
      badge: '第 1 步',
      title: '填写发布信息',
      desc: '给这次部署起个名字，填上你的名字，然后点下面绿色按钮。',
      btn: '下一步：创建发布单',
      showForm: true,
    },
    2: {
      badge: '第 2 步',
      title: '提交评审',
      desc: '发布单已创建。点按钮把它提交给评审流程。',
      btn: '下一步：提交评审',
      showForm: false,
    },
    3: {
      badge: '第 3 步',
      title: '完成审批',
      desc: '模拟两位评审通过 + 负责人终审（内网测试流程，点一次即可）。',
      btn: '下一步：完成审批',
      showForm: false,
    },
    4: {
      badge: '第 4 步',
      title: '部署到绿环境',
      desc: '将触发 GitHub Actions，把前后端代码部署到 149 的 :28080 绿环境。',
      btn: '开始部署到绿环境',
      showForm: false,
    },
  };

  const BLUE_STEPS = {
    1: {
      badge: '第 1 步',
      title: '填写发布信息',
      desc: '给这次蓝环境部署起个名字，填上你的名字，然后点下面蓝色按钮。',
      btn: '下一步：创建发布单',
      showForm: true,
    },
    2: {
      badge: '第 2 步',
      title: '提交评审',
      desc: '发布单已创建。点按钮把它提交给评审流程。',
      btn: '下一步：提交评审',
      showForm: false,
    },
    3: {
      badge: '第 3 步',
      title: '完成审批',
      desc: '模拟两位评审通过 + 负责人终审（内网测试流程，点一次即可）。',
      btn: '下一步：完成审批',
      showForm: false,
    },
    4: {
      badge: '第 4 步',
      title: '部署到蓝环境',
      desc: '将触发 GitHub Actions slot=blue，部署到 /opt/osh/app，验收 :58080。前提：生产流量须在绿环境。',
      btn: '开始部署到蓝环境',
      showForm: false,
    },
  };

  function releaseTarget(rel) {
    return rel?.deploy_target || 'green';
  }

  function filterByTarget(list, target) {
    return (list || []).filter((r) => releaseTarget(r) === target);
  }

  function navigate(page) {
    if (!PAGES[page]) return;
    state.page = page;
    localStorage.setItem('osh_page', page);
    if (location.hash !== `#${page}`) {
      history.replaceState(null, '', `#${page}`);
    }

    document.querySelectorAll('.nav-item').forEach((el) => {
      el.classList.toggle('active', el.dataset.page === page);
    });
    expandNavGroupForPage(page);
    document.querySelectorAll('.page').forEach((el) => {
      const active = el.dataset.page === page;
      el.classList.toggle('active', active);
      el.hidden = !active;
    });

    $('pageTitle').textContent = PAGES[page].title;
    $('pageSubtitle').textContent = PAGES[page].subtitle;

    if (page === 'traffic') {
      loadTrafficPage();
    }
    if (page === 'deploy-blue') {
      loadTrafficForBluePages();
      renderBlueUI();
      renderBlueDeployUI();
      loadBlueList();
    }
    if (page === 'sync-blue' || page === 'sql-blue') {
      loadTrafficForBluePages();
      if (page === 'sync-blue') loadBlueActive();
      if (page === 'sql-blue') renderBlueSqlUI();
    }
  }

  async function loadTrafficForBluePages() {
    await loadTraffic();
    if ((state.traffic?.active || 'unknown') === 'unknown') {
      setTimeout(loadTraffic, 2000);
    }
  }

  function setNavGroupOpen(group, open) {
    const el = document.querySelector(`.nav-group[data-group="${group}"]`);
    if (!el) return;
    const head = el.querySelector('.nav-group-head');
    const body = el.querySelector('.nav-group-body');
    if (!head || !body) return;
    head.setAttribute('aria-expanded', open ? 'true' : 'false');
    el.classList.toggle('open', open);
    if (open) {
      body.removeAttribute('hidden');
    } else {
      body.setAttribute('hidden', '');
    }
    localStorage.setItem(`osh_nav_${group}_open`, open ? '1' : '0');
  }

  function toggleNavGroup(group) {
    const el = document.querySelector(`.nav-group[data-group="${group}"]`);
    const willOpen = !el?.classList.contains('open');
    if (willOpen) {
      const other = group === 'green' ? 'blue' : 'green';
      setNavGroupOpen(other, false);
    }
    setNavGroupOpen(group, willOpen);
  }

  function expandNavGroupForPage(page) {
    const group = PAGE_GROUPS[page];
    if (group) {
      const other = group === 'green' ? 'blue' : 'green';
      setNavGroupOpen(other, false);
      setNavGroupOpen(group, true);
      return;
    }
    // 生产切流：收起蓝绿分组，减少干扰
    if (page === 'traffic') {
      setNavGroupOpen('green', false);
      setNavGroupOpen('blue', false);
    }
  }

  function initNavGroups() {
    const page = state.page || localStorage.getItem('osh_page') || 'deploy';
    const activeGroup = PAGE_GROUPS[page];

    ['green', 'blue'].forEach((group) => {
      if (page === 'traffic') {
        setNavGroupOpen(group, false);
        return;
      }
      if (activeGroup) {
        setNavGroupOpen(group, group === activeGroup);
        return;
      }
      const stored = localStorage.getItem(`osh_nav_${group}_open`);
      setNavGroupOpen(group, stored === null ? group === 'green' : stored === '1');
    });

    document.querySelectorAll('[data-toggle-group]').forEach((btn) => {
      btn.addEventListener('click', (e) => {
        e.preventDefault();
        e.stopPropagation();
        toggleNavGroup(btn.dataset.toggleGroup);
      });
    });
  }

  function initSidebar() {
    const layout = $('appLayout');
    if (localStorage.getItem('osh_sidebar_collapsed') === '1') {
      layout.classList.add('collapsed');
    }

    $('sidebarToggle').addEventListener('click', () => {
      layout.classList.toggle('collapsed');
      localStorage.setItem('osh_sidebar_collapsed', layout.classList.contains('collapsed') ? '1' : '0');
    });

    $('mobileMenuBtn').addEventListener('click', () => {
      layout.classList.toggle('mobile-open');
      $('sidebarBackdrop').hidden = !layout.classList.contains('mobile-open');
    });

    $('sidebarBackdrop').addEventListener('click', () => {
      layout.classList.remove('mobile-open');
      $('sidebarBackdrop').hidden = true;
    });

    document.querySelectorAll('.nav-item').forEach((btn) => {
      btn.addEventListener('click', () => {
        navigate(btn.dataset.page);
        layout.classList.remove('mobile-open');
        $('sidebarBackdrop').hidden = true;
      });
    });
  }

  function initPageFromHash() {
    const hash = (location.hash || '').replace('#', '').replace(/^\//, '');
    const page = PAGES[hash] ? hash : (localStorage.getItem('osh_page') || 'deploy');
    state.page = page;
    initNavGroups();
    navigate(page);
  }

  function isOtherDeployBusy(rel) {
    const current = rel || state.current;
    return state.activeDeploy?.busy && state.activeDeploy.id !== current?.id;
  }

  function isAnyDeployBusy() {
    return !!state.activeDeploy?.busy;
  }

  function isProductionGreen() {
    return (state.traffic?.active || '') === 'green';
  }

  function isBlueBusy() {
    return !!state.blueActive?.busy;
  }

  function canOperateBlue() {
    return isProductionGreen() && !isAnyDeployBusy() && !isBlueBusy() && !state.busy;
  }

  function canOperateBlueSql() {
    return canOperateBlue();
  }

  function productionTrafficBanner(context) {
    if (state.trafficLoading) {
      return { show: true, kind: 'loading', icon: '⏳', text: '正在检测当前生产流量…' };
    }
    const active = state.traffic?.active || 'unknown';
    if (active === 'green') {
      let text = '当前生产为绿环境，可更新部署蓝环境前后端';
      if (context === 'sync') text = '当前生产为绿环境，可执行绿 → 蓝全量数据同步';
      if (context === 'sql') text = '当前生产为绿环境，可对蓝待命库执行增量 SQL';
      return { show: true, kind: 'ok', icon: '✓', text };
    }
    if (active === 'blue') {
      return {
        show: true,
        kind: 'warn',
        icon: '⚠️',
        text: '当前生产为蓝环境，请先切流到绿环境后再操作蓝环境。',
      };
    }
    return {
      show: true,
      kind: 'warn',
      icon: '⚠️',
      text: '暂时无法确认生产流量，请稍后重试或到「生产切流」页查看。',
    };
  }

  function productionGuardMessage() {
    const active = state.traffic?.active || 'unknown';
    if (active === 'green') return '';
    if (active === 'blue') {
      return '当前生产为蓝环境，请先切流到绿环境后再操作蓝环境。';
    }
    return '暂时无法确认生产流量，请稍后重试。';
  }

  async function loadBlueActive() {
    try {
      state.blueActive = await api('/api/blue/active');
    } catch {
      state.blueActive = null;
    }
    renderBlueUI();
    renderBlueSqlUI();
    scheduleBluePoll();
  }

  function renderBlueSqlGuard() {
    const banner = $('blueSqlGuardBanner');
    const text = $('blueSqlGuardText');
    const iconEl = banner?.querySelector('.lock-icon');
    if (!banner || !text) return;
    const b = productionTrafficBanner('sql');
    banner.hidden = !b.show;
    banner.classList.remove('warn', 'ok', 'self', 'loading');
    if (b.kind === 'ok') banner.classList.add('ok');
    else if (b.kind === 'loading') banner.classList.add('self');
    else banner.classList.add('warn');
    if (iconEl) iconEl.textContent = b.icon;
    text.textContent = b.text;
  }

  function renderBlueSqlLock() {
    const banner = $('blueSqlLockBanner');
    const text = $('blueSqlLockText');
    if (!banner || !text) return;
    if (!isBlueBusy()) {
      banner.hidden = true;
      return;
    }
    const job = state.blueActive?.job;
    const kindLabel = job?.kind === 'deploy' ? '蓝环境部署' : '绿→蓝数据同步';
    banner.hidden = false;
    text.textContent = `${kindLabel}进行中，请等待完成后再执行 SQL`;
  }

  function renderBlueSqlUI() {
    renderBlueSqlGuard();
    renderBlueSqlLock();
    const hint = $('blueSqlHint');
    const btn = $('btnExecBlueSql');
    if (hint) {
      hint.textContent = isProductionGreen()
        ? '当前生产为绿环境，蓝库为待命库，可执行增量 SQL。'
        : productionGuardMessage() || '正在检测生产流量…';
    }
    if (btn) btn.disabled = !canOperateBlueSql();
  }

  function renderBlueGuard(prefix) {
    const banner = $(`${prefix}GuardBanner`);
    const text = $(`${prefix}GuardText`);
    const iconEl = banner?.querySelector('.lock-icon');
    if (!banner || !text) return;
    const ctx = prefix === 'blueSync' ? 'sync' : prefix === 'blueSql' ? 'sql' : 'deploy';
    const b = productionTrafficBanner(ctx);
    banner.hidden = !b.show;
    banner.classList.remove('warn', 'ok', 'self', 'loading');
    if (b.kind === 'ok') banner.classList.add('ok');
    else if (b.kind === 'loading') banner.classList.add('self');
    else banner.classList.add('warn');
    if (iconEl) iconEl.textContent = b.icon;
    text.textContent = b.text;
  }

  function renderBlueLock(prefix) {
    const banner = $(`${prefix}LockBanner`);
    const text = $(`${prefix}LockText`);
    if (!banner || !text) return;
    if (!isBlueBusy()) {
      banner.hidden = true;
      return;
    }
    const job = state.blueActive.job;
    const kindLabel = job.kind === 'deploy' ? '蓝环境部署' : '绿→蓝数据同步';
    banner.hidden = false;
    text.textContent = `${kindLabel}进行中：${job.message || '…'}`;
  }

  function renderBlueJobResult(resultId, kind) {
    const resultEl = $(resultId);
    const job = state.blueActive?.job;
    if (!resultEl || !job || job.kind !== kind) return;
    if (job.status === 'running') {
      resultEl.hidden = true;
      return;
    }
    if (job.status === 'success' || job.status === 'failed') {
      resultEl.hidden = false;
      resultEl.className = `sql-result ${job.status === 'success' ? 'ok' : 'err'}`;
      const icon = job.status === 'success' ? '✓' : '✗';
      resultEl.textContent = `${icon} ${job.message || job.status}\n${job.output || ''}`;
    }
  }

  function blueHintText() {
    if (!isProductionGreen()) return '生产流量不在绿环境，暂不可操作。';
    if (isAnyDeployBusy()) return '有发布单正在部署中，请等待完成。';
    if (isBlueBusy()) {
      const job = state.blueActive.job;
      return job.message || '数据同步进行中…';
    }
    return '执行 osh-prod-standby-sync.sh --green-to-blue，耗时约 5–20 分钟。';
  }

  function renderBlueSyncUI() {
    renderBlueGuard('blueSync');
    renderBlueLock('blueSync');

    const syncBtn = $('btnSyncBlue');
    const syncHint = $('blueSyncHint');
    const syncOk = isProductionGreen() && !isAnyDeployBusy() && !isBlueBusy() && !state.busy;

    if (syncBtn) {
      syncBtn.disabled = !syncOk;
      syncBtn.title = !isProductionGreen() ? '生产须在绿环境' : isAnyDeployBusy() ? '部署进行中' : isBlueBusy() ? '同步进行中' : '';
    }
    if (syncHint) syncHint.textContent = blueHintText();
    renderBlueJobResult('blueSyncResult', 'sync');
  }

  function renderBlueUI() {
    renderBlueGuard('blueDeploy');
    renderBlueDeployLock();
    renderBlueSyncUI();
    renderBlueSqlUI();
  }

  function renderBlueDeployLock() {
    const banner = $('blueDeployLockBanner');
    const text = $('blueDeployLockText');
    if (!banner || !text) return;
    if (!isAnyDeployBusy()) {
      banner.hidden = true;
      return;
    }
    const d = state.activeDeploy;
    const isOther = d?.id !== state.currentBlue?.id;
    banner.hidden = false;
    text.textContent = isOther
      ? `发布单「${d.title}」正在部署中，请等待完成后再发起新的部署`
      : `当前发布单正在部署中，请等待完成…`;
  }

  function renderBlueStepper(step) {
    document.querySelectorAll('#blueStepper .stepper-item').forEach((el) => {
      const n = Number(el.dataset.step);
      el.classList.remove('active', 'done');
      const adminSkip = isAdmin() && step === 4 && (n === 2 || n === 3);
      if (step === 0 || (step > 0 && n < step) || adminSkip) el.classList.add('done');
      if (n === step) el.classList.add('active');
      if (step === -1 && n === 4) el.classList.add('active');
    });
  }

  function renderBlueDeployUI() {
    const rel = state.currentBlue;
    const step = currentStep(rel);
    let cfg = BLUE_STEPS[step] || BLUE_STEPS[4];
    const inProgress = isDeployInProgress(rel);
    const prodBlocked = !isProductionGreen() && step === 4 && !inProgress && step !== 0;

    renderBlueStepper(step === 0 ? 4 : step);
    renderBlueGuard('blueDeploy');
    renderBlueDeployLock();

    $('blueActionForm').style.display = step === 1 && cfg.showForm ? 'block' : 'none';
    $('blueSuccessBox').hidden = step !== 0;
    $('blueWaitingBox').hidden = !inProgress;
    $('blueMainBtn').hidden = step === 0 || step === -1 || inProgress || (step === 4 && isOtherDeployBusy(rel)) || prodBlocked;
    const resetBtn = $('btnNewBlueDeploy');
    if (resetBtn) resetBtn.hidden = !(step === 0 || step === -1) || isAnyDeployBusy();

    if (step === 0) {
      const auto = autoTestStep(rel);
      const testWarn = auto?.status === 'failed' || rel?.status === 'failed';
      if (resetBtn) resetBtn.textContent = '再部署一遍 →';
      $('blueStepBadge').textContent = '完成';
      $('blueActionTitle').textContent = testWarn ? '蓝环境已部署（测试有警告）' : '部署完成';
      $('blueActionDesc').textContent = testWarn
        ? '代码已成功部署到蓝环境。自动测试未完全通过，请手动打开 :58080 验收。'
        : `发布单「${rel?.title || ''}」已部署到蓝环境，可以去验收了。`;
      return;
    }

    if (step === -1) {
      $('blueStepBadge').textContent = '失败';
      $('blueActionTitle').textContent = '部署失败';
      $('blueActionDesc').textContent = rel?.steps?.find((s) => s.status === 'failed')?.message || '请展开下方日志查看原因。';
      $('blueMainBtn').hidden = true;
      if (resetBtn) resetBtn.textContent = '重新开始 →';
      return;
    }

    if (prodBlocked) {
      $('blueStepBadge').textContent = '第 4 步';
      $('blueActionTitle').textContent = '暂不可部署';
      $('blueActionDesc').textContent = productionGuardMessage() || '生产流量不在绿环境，请先切流后再部署蓝环境。';
      return;
    }

    if (step === 4 && isOtherDeployBusy(rel) && !inProgress) {
      $('blueStepBadge').textContent = '第 4 步';
      $('blueActionTitle').textContent = '暂不可部署';
      $('blueActionDesc').textContent = `发布单「${state.activeDeploy.title}」正在部署中，同一时间只能跑一个部署任务，请等待完成。`;
      return;
    }

    if (rel && inProgress) {
      const deploy = deployStep(rel);
      if (deploy?.status === 'success') {
        $('blueStepBadge').textContent = '第 4 步';
        $('blueActionTitle').textContent = '正在自动测试…';
        $('blueActionDesc').textContent = '蓝环境代码已就绪，正在跑自动测试，请稍候。';
        $('blueWaitingText').textContent = '自动测试进行中…';
        return;
      }
      $('blueStepBadge').textContent = '第 4 步';
      $('blueActionTitle').textContent = rel.status === 'deploying' ? '正在部署…' : '正在自动测试…';
      $('blueActionDesc').textContent = '无需操作，等待即可。';
      $('blueWaitingText').textContent = rel.status === 'deploying'
        ? 'GitHub Actions 正在跑前后端 workflow…（约 3–8 分钟）'
        : '蓝环境已就绪，正在跑自动测试…';
      return;
    }

    if (step === 3 && rel && reviewsOK(rel.items?.[0]) && !rel.boss_approved) {
      $('blueStepBadge').textContent = '第 3 步';
      $('blueActionForm').style.display = 'none';
      if (isBoss()) {
        $('blueActionTitle').textContent = '终审发布';
        $('blueActionDesc').textContent = '双评审已通过，请确认终审后进入部署。';
        $('blueMainBtnText').textContent = '终审通过 →';
        $('blueMainBtn').hidden = false;
      } else {
        $('blueActionTitle').textContent = '等待终审';
        $('blueActionDesc').textContent = `双评审已通过，需 ${state.health?.deploy?.boss_reviewer || 'juege'} 登录终审后才能部署。`;
        $('blueMainBtn').hidden = true;
      }
      return;
    }

    if (step === 1 && isAdmin()) {
      cfg = { ...cfg, desc: '管理员账号可直接创建并部署，无需双评审与终审。' };
    }

    $('blueStepBadge').textContent = cfg.badge;
    $('blueActionTitle').textContent = cfg.title;
    $('blueActionDesc').textContent = cfg.desc;
    $('blueMainBtnText').textContent = cfg.btn;
  }

  function renderBlueLogs(rel) {
    const steps = rel?.steps || [];
    $('blueSteps').innerHTML = steps.length
      ? steps.map((s) => `
        <li>
          <span class="dot ${s.status}"></span>
          <div>
            <div class="step-title">${escapeHtml(s.title)}</div>
            <div class="step-msg">${escapeHtml(s.status)} ${escapeHtml(s.message || '')}</div>
          </div>
        </li>`).join('')
      : '<li><div class="empty">暂无日志，完成第 1 步后这里会显示进度</div></li>';
  }

  async function loadBlueList() {
    const list = filterByTarget(await api('/api/releases'), 'blue');
    const el = $('blueList');
    if (!list.length) {
      el.innerHTML = '<div class="empty">还没有蓝环境发布记录</div>';
      return;
    }
    el.innerHTML = list.map((r) => `
      <button type="button" class="release-item ${state.currentBlue?.id === r.id ? 'active' : ''}" data-id="${r.id}">
        <strong>${escapeHtml(r.title)}</strong>
        <span class="meta">${escapeHtml(r.status)} · ${new Date(r.updated_at).toLocaleString()}</span>
      </button>`).join('');
    el.querySelectorAll('.release-item').forEach((node) => {
      node.addEventListener('click', () => selectBlue(node.dataset.id));
    });
  }

  async function selectBlue(id) {
    state.currentBlue = await api(`/api/releases/${id}`);
    renderBlueDeployUI();
    renderBlueLogs(state.currentBlue);
    await loadBlueList();
    scheduleBlueDeployPoll();
    toast('已切换到：' + state.currentBlue.title);
  }

  function resetBlueDeploy() {
    if (isAnyDeployBusy()) {
      toast('有部署任务进行中，请等待完成后再新建', 'error');
      return;
    }
    if (state.blueDeployPollTimer) {
      clearInterval(state.blueDeployPollTimer);
      state.blueDeployPollTimer = null;
    }
    state.currentBlue = null;
    $('blueTitle').value = '';
    $('blueLogFold').open = false;
    renderBlueDeployUI();
    renderBlueLogs(null);
    loadBlueList();
    toast('已重置，请填写新的发布名称开始蓝环境部署');
  }

  async function autoPickBlueRelease(list) {
    const blueList = filterByTarget(list, 'blue');
    const active = blueList.find((r) => {
      const step = currentStep(r);
      return step > 0 || isDeployInProgress(r);
    });
    if (active) await selectBlue(active.id);
  }

  async function createBlueRelease() {
    const title = $('blueTitle').value.trim();
    if (!title) throw new Error('请填写发布名称');
    const author = $('blueAuthor').value.trim();
    if (!author) throw new Error('请填写你的名字');
    const rel = await api('/api/releases', {
      method: 'POST',
      body: JSON.stringify({
        title,
        commit_sha: 'blue-deploy',
        author,
        deploy_target: 'blue',
        level: 'normal',
        repo: 'juege-osh/osh',
        items: [{
          title: '前后端蓝环境部署',
          type: 'code',
          ref: 'deploy-149',
          developer: $('developer').value,
          expected_impact: '同步蓝环境代码，不影响生产',
          reviewer1: $('rev1').value,
          reviewer2: $('rev2').value,
        }],
      }),
    });
    state.currentBlue = rel;
    toast('第 1 步完成！继续点蓝色按钮');
  }

  async function submitBlueReview() {
    const actor = $('blueAuthor').value.trim() || 'ops';
    state.currentBlue = await api(`/api/releases/${state.currentBlue.id}/submit-review`, {
      method: 'POST',
      body: JSON.stringify({ actor }),
    });
    toast('第 2 步完成！继续点蓝色按钮');
  }

  async function completeBlueApproval() {
    const item = state.currentBlue.items[0];
    if (!reviewsOK(item)) {
      for (const reviewer of [item.reviewer1, item.reviewer2]) {
        await api(`/api/items/${item.id}/reviews`, {
          method: 'POST',
          body: JSON.stringify({
            reviewer, tested: true,
            demo_seen: reviewer !== item.developer,
            result: 'approve', comment: '通过',
          }),
        });
      }
    }
    state.currentBlue = await api(`/api/releases/${state.currentBlue.id}`);
    if (isBoss()) {
      state.currentBlue = await api(`/api/releases/${state.currentBlue.id}/boss-approve`, {
        method: 'POST',
        body: JSON.stringify({ reviewer: state.user.username, comment: '终审通过' }),
      });
      toast('终审通过，可以部署了');
    } else {
      toast('双评审已提交，等待 juege 终审');
    }
  }

  async function bossApproveBlueOnly() {
    state.currentBlue = await api(`/api/releases/${state.currentBlue.id}/boss-approve`, {
      method: 'POST',
      body: JSON.stringify({ reviewer: state.user.username, comment: '终审通过' }),
    });
    toast('终审通过，可以部署了');
  }

  async function deployBlueRelease() {
    if (isOtherDeployBusy(state.currentBlue)) {
      throw new Error(`发布单「${state.activeDeploy.title}」正在部署中，请等待完成`);
    }
    if (!isProductionGreen()) {
      throw new Error(productionGuardMessage() || '生产流量不在绿环境');
    }
    const actor = $('blueAuthor').value.trim() || 'ops';
    state.currentBlue = { ...state.currentBlue, status: 'deploying' };
    state.activeDeploy = {
      busy: true,
      id: state.currentBlue.id,
      title: state.currentBlue.title,
      status: 'deploying',
    };
    renderBlueDeployUI();
    renderBlueDeployLock();

    const rel = await api(`/api/releases/${state.currentBlue.id}/deploy`, {
      method: 'POST',
      body: JSON.stringify({ actor }),
    });
    state.currentBlue = rel;
    toast('已触发蓝环境部署，正在等待 GitHub Actions…');
    $('blueLogFold').open = true;
    await loadActiveDeploy();
    scheduleBlueDeployPoll();
  }

  async function handleBlueMainAction() {
    if (state.busy) return;
    state.busy = true;
    $('loading').classList.add('show');
    $('blueMainBtn').disabled = true;
    try {
      const step = currentStep(state.currentBlue);
      if (step === 1) await createBlueRelease();
      else if (step === 2) await submitBlueReview();
      else if (step === 3) {
        if (reviewsOK(state.currentBlue.items?.[0]) && isBoss()) await bossApproveBlueOnly();
        else await completeBlueApproval();
      }
      else if (step === 4) {
        $('loading').classList.remove('show');
        await deployBlueRelease();
      }
      renderBlueDeployUI();
      renderBlueLogs(state.currentBlue);
      await loadBlueList();
      if (currentStep(state.currentBlue) > 1 && currentStep(state.currentBlue) < 5) {
        $('blueLogFold').open = true;
      }
    } catch (err) {
      toast(err.message || String(err), 'error');
    } finally {
      state.busy = false;
      $('loading').classList.remove('show');
      $('blueMainBtn').disabled = false;
      renderBlueUI();
    }
  }

  function scheduleBlueDeployPoll() {
    if (state.blueDeployPollTimer) clearInterval(state.blueDeployPollTimer);
    state.blueDeployPollTimer = null;
    if (!state.currentBlue) return;
    const step = currentStep(state.currentBlue);
    if (!isDeployInProgress(state.currentBlue) && step !== 4) return;
    state.blueDeployPollTimer = setInterval(async () => {
      try {
        if (!state.currentBlue) return;
        state.currentBlue = await api(`/api/releases/${state.currentBlue.id}`);
        renderBlueDeployUI();
        renderBlueLogs(state.currentBlue);
        await loadBlueList();
        await loadActiveDeploy();
        if (!isDeployInProgress(state.currentBlue)) {
          clearInterval(state.blueDeployPollTimer);
          state.blueDeployPollTimer = null;
          const step = currentStep(state.currentBlue);
          if (step === 0) toast('蓝环境部署完成！请打开 :58080 验收');
          else if (state.currentBlue.status === 'failed') toast('蓝环境部署失败，请查看日志', 'error');
        }
      } catch { /* ignore */ }
    }, 3000);
  }

  function scheduleBluePoll() {
    if (state.bluePollTimer) clearInterval(state.bluePollTimer);
    state.bluePollTimer = null;
    if (!isBlueBusy()) return;
    state.bluePollTimer = setInterval(async () => {
      await loadBlueActive();
      if (!isBlueBusy()) {
        clearInterval(state.bluePollTimer);
        state.bluePollTimer = null;
        const job = state.blueActive?.job;
        if (job?.status === 'success') toast(job.message || '蓝环境任务完成');
        else if (job?.status === 'failed') toast(job.message || '蓝环境任务失败', 'error');
      }
    }, 4000);
  }

  async function executeBlueCustomSql() {
    if (!canOperateBlueSql()) {
      toast(productionGuardMessage() || '当前不可对蓝库执行 SQL', 'error');
      return;
    }
    if (state.busy) return;
    const sql = $('blueCustomSql')?.value.trim();
    if (!sql) { toast('请先填写 SQL', 'error'); return; }
    const actor = $('author')?.value.trim() || 'ops';
    const label = $('blueSqlLabel')?.value.trim() || 'custom';
    if (!confirm('确认将以下 SQL 执行到蓝环境 MySQL？\n\n目标：osh-mysql / backstage（待命库）\n\n增量更新，不可自动回滚。')) return;

    state.busy = true;
    $('loading')?.classList.add('show');
    const btn = $('btnExecBlueSql');
    if (btn) btn.disabled = true;
    const resultEl = $('blueSqlResult');
    if (resultEl) resultEl.hidden = true;
    try {
      const res = await api('/api/blue/sql/execute', {
        method: 'POST',
        body: JSON.stringify({ sql, actor, label }),
      });
      if (resultEl) {
        resultEl.hidden = false;
        resultEl.textContent = `✓ 执行成功\n${res.output || ''}`;
        resultEl.className = 'sql-result ok';
      }
      toast('SQL 已执行到蓝库');
    } catch (err) {
      if (resultEl) {
        resultEl.hidden = false;
        resultEl.textContent = `✗ 执行失败\n${formatSqlError(err.message || String(err))}`;
        resultEl.className = 'sql-result err';
      }
      toast(formatSqlError(err.message || String(err)).split('\n')[0], 'error');
    } finally {
      state.busy = false;
      $('loading')?.classList.remove('show');
      renderBlueSqlUI();
    }
  }

  async function syncBlue() {
    if (!canOperateBlue()) {
      toast(productionGuardMessage() || blueHintText(), 'error');
      return;
    }
    const actor = $('author').value.trim() || 'ops';
    const reason = $('blueSyncReason').value.trim();
    if (!confirm('确认执行绿→蓝数据同步？\n\n将同步 MySQL、Nacos、ES、Redis 到蓝环境待命库，耗时约 5–20 分钟。\n\n前提：当前生产流量须在绿环境。')) return;

    state.busy = true;
    $('loading').classList.add('show');
    const resultEl = $('blueSyncResult');
    resultEl.hidden = true;
    renderBlueUI();
    try {
      const job = await api('/api/blue/sync', {
        method: 'POST',
        body: JSON.stringify({ actor, reason }),
      });
      state.blueActive = { busy: true, job };
      toast('已启动绿→蓝数据同步…');
      renderBlueUI();
      scheduleBluePoll();
      await loadBlueActive();
    } catch (err) {
      toast(err.message || String(err), 'error');
      renderBlueUI();
    } finally {
      state.busy = false;
      $('loading').classList.remove('show');
      renderBlueUI();
    }
  }

  async function loadActiveDeploy() {
    try {
      const d = await api('/api/deploy/active');
      state.activeDeploy = d.busy ? d : null;
    } catch {
      state.activeDeploy = null;
    }
    renderDeployLock();
    renderBlueDeployLock();
    scheduleActivePoll();
  }

  function renderDeployLock() {
    const banner = $('deployLockBanner');
    const text = $('deployLockText');
    if (!banner || !text) return;

    if (!isAnyDeployBusy()) {
      banner.hidden = true;
      return;
    }

    const d = state.activeDeploy;
    const isOther = isOtherDeployBusy(state.current);
    banner.hidden = false;
    banner.classList.toggle('self', !isOther);
    text.textContent = isOther
      ? `发布单「${d.title}」正在部署中，请等待完成后再发起新的部署`
      : `当前发布单正在部署中，请等待完成…`;
  }

  function scheduleActivePoll() {
    if (state.activePollTimer) clearInterval(state.activePollTimer);
    state.activePollTimer = null;
    if (!isAnyDeployBusy()) return;
    state.activePollTimer = setInterval(async () => {
      await loadActiveDeploy();
      if (!isAnyDeployBusy()) {
        clearInterval(state.activePollTimer);
        state.activePollTimer = null;
        renderUI();
        renderBlueDeployUI();
      }
    }, 4000);
  }

  async function api(path, opts = {}) {
    const res = await fetch(path, {
      ...opts,
      headers: { ...authHeaders(), ...(opts.headers || {}) },
    });
    let data = {};
    try { data = await res.json(); } catch { /* empty */ }
    if (!res.ok) throw new Error(data.error || res.statusText);
    return data;
  }

  function toast(msg, type = 'success') {
    const el = document.createElement('div');
    el.className = `toast ${type}`;
    el.textContent = msg;
    $('toasts').appendChild(el);
    setTimeout(() => el.remove(), 4000);
  }

  function escapeHtml(s) {
    return String(s).replace(/&/g, '&amp;').replace(/</g, '&lt;').replace(/>/g, '&gt;');
  }

  function reviewsOK(item) {
    if (!item) return false;
    const need = new Set([item.reviewer1, item.reviewer2].filter(Boolean));
    const ok = new Set();
    for (const rv of item.reviews || []) {
      if (rv.result === 'approve' && rv.tested) ok.add(rv.reviewer);
    }
    return need.size === 2 && [...need].every((r) => ok.has(r));
  }

  function autoTestStep(rel) {
    return (rel?.steps || []).find((s) => s.step_key === 'auto_test');
  }

  function deployStep(rel) {
    return (rel?.steps || []).find((s) => s.step_key === 'deploy_standby');
  }

  /** 当前应该做第几步（1-4），0=已完成，-1=失败 */
  function currentStep(rel) {
    if (!rel) return 1;
    const deploy = deployStep(rel);
    const auto = autoTestStep(rel);

    if (rel.status === 'done') return 0;
    if (deploy?.status === 'success' && auto?.status === 'success') return 0;
    if (rel.status === 'testing' && auto?.status === 'success') return 0;

    if (rel.status === 'failed') {
      if (deploy?.status === 'success') return 0; // 绿环境已部署，仅测试/旁路失败
      return -1;
    }

    if (['deploying', 'testing'].includes(rel.status)) {
      if (deploy?.status === 'success' && auto?.status !== 'success') return 4;
      return 4;
    }

    if (rel.boss_approved && reviewsOK(rel.items?.[0])) return 4;
    if (isAdmin() && rel.boss_approved && ['draft', 'approved'].includes(rel.status)) return 4;
    if (reviewsOK(rel.items?.[0]) && !rel.boss_approved) return 3;
    if (rel.status === 'draft') return isAdmin() && rel.boss_approved ? 4 : 2;
    if (rel.status === 'reviewing') return 3;
    return 2;
  }

  function renderStepper(step) {
    document.querySelectorAll('#stepper .stepper-item, #pageDeploy .stepper-item').forEach((el) => {
      const n = Number(el.dataset.step);
      el.classList.remove('active', 'done');
      const adminSkip = isAdmin() && step === 4 && (n === 2 || n === 3);
      if (step === 0 || (step > 0 && n < step) || adminSkip) el.classList.add('done');
      if (n === step) el.classList.add('active');
      if (step === -1 && n === 4) el.classList.add('active');
    });
  }

  function isDeployInProgress(rel) {
    if (!rel) return false;
    if (currentStep(rel) !== 4) return false;
    const deploy = deployStep(rel);
    const auto = autoTestStep(rel);
    if (rel.status === 'deploying' && deploy?.status !== 'success') return true;
    if (rel.status === 'testing' && deploy?.status === 'success' && auto?.status !== 'success') return true;
    if (deploy?.status === 'running' || auto?.status === 'running') return true;
    return false;
  }

  function renderUI() {
    const rel = state.current;
    const step = currentStep(rel);
    let cfg = STEPS[step] || STEPS[4];
    const inProgress = isDeployInProgress(rel);

    renderStepper(step === 0 ? 4 : step);

    $('actionForm').style.display = step === 1 && cfg.showForm ? 'block' : 'none';
    $('successBox').hidden = step !== 0;
    $('waitingBox').hidden = !inProgress;
    $('mainBtn').hidden = step === 0 || step === -1 || inProgress || (step === 4 && isOtherDeployBusy(rel));
    const resetBtn = $('btnNewDeploy');
    if (resetBtn) resetBtn.hidden = !(step === 0 || step === -1) || isAnyDeployBusy();

    if (step === 0) {
      const deploy = deployStep(rel);
      const auto = autoTestStep(rel);
      const testWarn = auto?.status === 'failed' || rel?.status === 'failed';
      if (resetBtn) resetBtn.textContent = '再部署一遍 →';
      $('stepBadge').textContent = '完成';
      $('actionTitle').textContent = testWarn ? '绿环境已部署（测试有警告）' : '部署完成';
      $('actionDesc').textContent = testWarn
        ? '代码已成功部署到绿环境。自动测试未完全通过，请手动打开绿环境验收。'
        : `发布单「${rel?.title || ''}」已部署到绿环境，可以去验收了。`;
      return;
    }

    if (step === -1) {
      $('stepBadge').textContent = '失败';
      $('actionTitle').textContent = '部署失败';
      $('actionDesc').textContent = rel?.steps?.find((s) => s.status === 'failed')?.message || '请展开下方日志查看原因，或到 GitHub Actions 查日志。';
      $('mainBtn').hidden = true;
      if (resetBtn) resetBtn.textContent = '重新开始 →';
      return;
    }

    if (step === 4 && isOtherDeployBusy(rel) && !inProgress) {
      $('stepBadge').textContent = '第 4 步';
      $('actionTitle').textContent = '暂不可部署';
      $('actionDesc').textContent = `发布单「${state.activeDeploy.title}」正在部署中，同一时间只能跑一个部署任务，请等待完成。`;
      return;
    }

    if (rel && inProgress) {
      const deploy = deployStep(rel);
      if (deploy?.status === 'success') {
        $('stepBadge').textContent = '第 4 步';
        $('actionTitle').textContent = '正在自动测试…';
        $('actionDesc').textContent = '绿环境代码已就绪，正在跑自动测试，请稍候。';
        $('waitingText').textContent = '自动测试进行中…（analyzer 未启动时会自动跳过）';
        return;
      }
      $('stepBadge').textContent = '第 4 步';
      $('actionTitle').textContent = rel.status === 'deploying' ? '正在部署…' : '正在自动测试…';
      $('actionDesc').textContent = '无需操作，等待即可。';
      $('waitingText').textContent = rel.status === 'deploying'
        ? 'GitHub Actions 正在跑前后端 workflow…（约 3–8 分钟，跑完才会显示成功）'
        : '绿环境已就绪，正在跑自动测试…';
      return;
    }

    if (step === 3 && rel && reviewsOK(rel.items?.[0]) && !rel.boss_approved) {
      $('stepBadge').textContent = '第 3 步';
      $('actionForm').style.display = 'none';
      if (isBoss()) {
        $('actionTitle').textContent = '终审发布';
        $('actionDesc').textContent = '双评审已通过，请确认终审后进入部署。';
        $('mainBtnText').textContent = '终审通过 →';
        $('mainBtn').hidden = false;
      } else {
        $('actionTitle').textContent = '等待终审';
        $('actionDesc').textContent = `双评审已通过，需 ${state.health?.deploy?.boss_reviewer || 'juege'} 登录终审后才能部署。`;
        $('mainBtn').hidden = true;
      }
      return;
    }

    if (step === 1 && isAdmin()) {
      cfg = { ...cfg, desc: '管理员账号可直接创建并部署，无需双评审与终审。' };
    }

    $('stepBadge').textContent = cfg.badge;
    $('actionTitle').textContent = cfg.title;
    $('actionDesc').textContent = cfg.desc;
    $('mainBtnText').textContent = cfg.btn;
    renderDeployLock();
    renderTrafficUI();
  }

  function renderLogs(rel) {
    const steps = rel?.steps || [];
    $('steps').innerHTML = steps.length
      ? steps.map((s) => `
        <li>
          <span class="dot ${s.status}"></span>
          <div>
            <div class="step-title">${escapeHtml(s.title)}</div>
            <div class="step-msg">${escapeHtml(s.status)} ${escapeHtml(s.message || '')}</div>
          </div>
        </li>`).join('')
      : '<li><div class="empty">暂无日志，完成第 1 步后这里会显示进度</div></li>';
  }

  async function loadHealth() {
    const h = await api('/api/health');
    state.health = h;
    const d = h.deploy || {};
    const mysql = h.mysql || {};
    $('modeBadge').textContent = h.mock_mode ? '演示模式' : '已连接';
    $('modeBadge').className = `badge ${h.mock_mode ? 'mock' : 'live'}`;
    $('ghaBadge').textContent = d.gha_enabled ? 'GitHub 部署' : 'GHA 未配置';
    $('ghaBadge').className = `badge ${d.gha_enabled ? 'live' : 'offline'}`;

    if (d.green_url) {
      $('greenLink').href = d.green_url;
      $('footerGreenLink').href = d.green_url;
      $('footerGreenLink').textContent = d.green_url;
    }
    const blueUrl = `http://${d.prod_host || '149.88.92.159'}:58080/`;
    const blueAccept = $('blueAcceptLink');
    if (blueAccept) blueAccept.href = blueUrl;
    $('footerBackend').textContent = d.backend_ref || '—';
    $('footerFrontend').textContent = d.frontend_ref || '—';

    const blueTip = $('blueDeployTip');
    if (blueTip) {
      blueTip.textContent = `目标：/opt/osh/app · 验收 http://${d.prod_host || '149.88.92.159'}:58080/ · 分支 ${d.backend_ref || '—'} / ${d.frontend_ref || '—'}`;
    }
    const blueSyncTip = $('blueSyncTip');
    if (blueSyncTip) {
      blueSyncTip.textContent = `方向：绿（生产）→ 蓝（待命）· 脚本 osh-prod-standby-sync.sh --green-to-blue`;
    }

    const tip = $('sqlTip');
    if (tip) {
      tip.textContent = mysql.configured
        ? `目标：${mysql.green_container || 'osh-g-mysql'} / ${mysql.green_database || 'backstage'}`
        : '请在 config.env 配置 GREEN_MYSQL_ROOT_PASSWORD 后才能执行 SQL。';
    }
    const blueSqlTip = $('blueSqlTip');
    if (blueSqlTip) {
      blueSqlTip.textContent = mysql.configured
        ? `目标：${mysql.blue_container || 'osh-mysql'} / ${mysql.blue_database || 'backstage'} · 增量 SQL，非全量同步`
        : '请在 config.env 配置 BLUE_MYSQL_ROOT_PASSWORD（或与绿库相同）后才能执行。';
    }
  }

  async function loadSqlTemplates() {
    const sels = [$('sqlTemplate'), $('blueSqlTemplate')].filter(Boolean);
    if (!sels.length) return;
    try {
      const list = await api('/api/migrations');
      const options = '<option value="">从模板填入（可选）</option>' +
        list.map((m) => `<option value="${escapeHtml(m.id)}">${escapeHtml(m.description || m.name)}</option>`).join('');
      sels.forEach((sel) => { sel.innerHTML = options; });
    } catch {
      sels.forEach((sel) => { sel.innerHTML = '<option value="">无可用模板</option>'; });
    }
  }

  async function loadTemplateIntoEditor() {
    const id = $('sqlTemplate').value;
    if (!id) { toast('请先选择模板', 'error'); return; }
    const data = await api(`/api/migrations/${id}`);
    $('customSql').value = data.sql || '';
    if (!$('sqlLabel').value.trim()) $('sqlLabel').value = id;
    toast('模板已填入，请检查后再执行');
  }

  async function loadBlueTemplateIntoEditor() {
    const id = $('blueSqlTemplate')?.value;
    if (!id) { toast('请先选择模板', 'error'); return; }
    const data = await api(`/api/migrations/${id}`);
    $('blueCustomSql').value = data.sql || '';
    if (!$('blueSqlLabel')?.value.trim()) $('blueSqlLabel').value = id;
    toast('模板已填入蓝库编辑器，请检查后再执行');
  }

  function formatSqlError(msg) {
    const raw = String(msg || '');
    const lines = raw.split('\n').filter((l) => !l.includes('Using a password on the command line'));
    const text = lines.join('\n').trim() || raw;
    if (/1050.*already exists/i.test(text)) {
      const m = text.match(/Table '([^']+)' already exists/i);
      const tbl = m ? m[1] : '该表';
      return `表 ${tbl} 已存在，无需重复建表。\n\n若只是确认结构，可执行：\nSHOW CREATE TABLE ${tbl};\n\n若要改字段，请用 ALTER TABLE，或从模板选「001」使用 CREATE TABLE IF NOT EXISTS。\n\n---\n${text}`;
    }
    if (/1060.*Duplicate column/i.test(text)) {
      return `字段已存在，无需重复 ADD COLUMN。\n\n---\n${text}`;
    }
    if (/1091.*Can't DROP/i.test(text)) {
      return `要删除的字段/索引不存在，请先用 SHOW COLUMNS 确认当前表结构。\n\n---\n${text}`;
    }
    return text;
  }

  async function executeCustomSql() {
    if (state.busy) return;
    const sql = $('customSql').value.trim();
    if (!sql) { toast('请先填写 SQL', 'error'); return; }
    const actor = $('author').value.trim() || 'ops';
    const label = $('sqlLabel').value.trim() || 'custom';
    if (!confirm(`确认将以下 SQL 执行到绿环境 MySQL？\n\n目标：osh-g-mysql / backstage\n\n此操作不可自动回滚。`)) return;

    state.busy = true;
    $('loading').classList.add('show');
    $('btnExecSql').disabled = true;
    const resultEl = $('sqlResult');
    resultEl.hidden = true;
    try {
      const res = await api('/api/sql/execute', {
        method: 'POST',
        body: JSON.stringify({ sql, actor, label }),
      });
      resultEl.hidden = false;
      resultEl.textContent = `✓ 执行成功\n${res.output || ''}`;
      resultEl.className = 'sql-result ok';
      toast('SQL 已执行到绿库');
    } catch (err) {
      resultEl.hidden = false;
      resultEl.textContent = `✗ 执行失败\n${formatSqlError(err.message || String(err))}`;
      resultEl.className = 'sql-result err';
      toast(formatSqlError(err.message || String(err)).split('\n')[0], 'error');
    } finally {
      state.busy = false;
      $('loading').classList.remove('show');
      $('btnExecSql').disabled = false;
    }
  }

  async function loadTraffic() {
    state.trafficLoading = true;
    renderBlueUI();
    renderBlueDeployUI();
    try {
      const t = await api('/api/traffic/status');
      state.traffic = t;
      const active = t.active || 'unknown';
      const label = active === 'green' ? '生产流量：绿' : active === 'blue' ? '生产流量：蓝（绿环境仅预发）' : '生产流量：未知';
      $('footerTraffic').textContent = label;
      renderTrafficUI();
      renderBlueUI();
      renderBlueDeployUI();
    } catch {
      state.traffic = null;
      $('footerTraffic').textContent = '生产流量：检测失败';
      renderBlueUI();
      renderBlueDeployUI();
    } finally {
      state.trafficLoading = false;
      renderBlueUI();
      renderBlueDeployUI();
    }
  }

  function renderTrafficUI() {
    const pill = $('trafficActivePill');
    const desc = $('trafficStatusDesc');
    const raw = $('trafficRaw');
    const btnGreen = $('btnToGreen');
    const btnBlue = $('btnToBlue');
    const lockBanner = $('trafficLockBanner');
    const lockText = $('trafficLockText');
    if (!pill) return;

    const t = state.traffic || {};
    const active = t.active || 'unknown';
    const busy = isAnyDeployBusy();

    pill.textContent = active === 'green' ? '绿环境' : active === 'blue' ? '蓝环境' : '未知';
    pill.className = `traffic-pill ${active}`;

    if (active === 'green') {
      desc.textContent = '当前 :80 入口指向绿环境，用户访问的是绿库与绿应用。';
    } else if (active === 'blue') {
      desc.textContent = '当前 :80 入口指向蓝环境（正常生产）。绿环境 :28080 仅用于预发验收。';
    } else {
      desc.textContent = '无法解析当前流量状态，请展开下方查看脚本输出。';
    }

    if (raw) raw.textContent = t.raw || '—';

    if (lockBanner && lockText) {
      lockBanner.hidden = !busy;
      if (busy) {
        lockText.textContent = `发布单「${state.activeDeploy?.title || ''}」正在部署中，请等待完成后再切流`;
      }
    }

    if (btnGreen) {
      btnGreen.disabled = busy || active === 'green' || state.busy;
      btnGreen.title = active === 'green' ? '已在绿环境' : busy ? '部署进行中' : '';
    }
    if (btnBlue) {
      btnBlue.disabled = busy || active === 'blue' || state.busy;
      btnBlue.title = active === 'blue' ? '已在蓝环境' : busy ? '部署进行中' : '';
    }
    renderBlueUI();
  }

  async function loadTrafficHistory() {
    const el = $('trafficHistory');
    if (!el) return;
    try {
      const list = await api('/api/traffic/history');
      if (!list.length) {
        el.innerHTML = '<div class="empty">暂无切流记录</div>';
        return;
      }
      el.innerHTML = list.map((e) => {
        const time = new Date(e.created_at).toLocaleString();
        const reason = e.reason ? ` · ${escapeHtml(e.reason)}` : '';
        return `
          <div class="traffic-history-item">
            <strong>${escapeHtml(e.actor || 'ops')}</strong>
            <span class="arrow">${escapeHtml(e.from_slot)} → ${escapeHtml(e.to_slot)}</span>
            <span class="meta">${time}${reason}</span>
          </div>`;
      }).join('');
    } catch {
      el.innerHTML = '<div class="empty">加载失败</div>';
    }
  }

  async function loadTrafficPage() {
    await loadTraffic();
    await loadTrafficHistory();
    renderTrafficUI();
  }

  async function switchTraffic(target) {
    if (state.busy) return;
    if (isAnyDeployBusy()) {
      toast('有部署任务进行中，请等待完成后再切流', 'error');
      return;
    }

    const active = state.traffic?.active;
    if (target === 'green' && active === 'green') {
      toast('当前已在绿环境', 'error');
      return;
    }
    if (target === 'blue' && active === 'blue') {
      toast('当前已在蓝环境', 'error');
      return;
    }

    const toLabel = target === 'green' ? '绿环境' : '蓝环境';
    const fromLabel = active === 'green' ? '绿' : active === 'blue' ? '蓝' : '当前';
    const reason = $('trafficReason')?.value.trim() || '';
    const actor = $('author').value.trim() || 'ops';

    const warn = target === 'green'
      ? '确认将生产 :80 切到绿环境？\n\n请确保绿环境已部署并通过验收。'
      : '确认将生产 :80 切回蓝环境？\n\n将执行 to-blue --resume-cron 恢复定时同步。';
    if (!confirm(`${warn}\n\n${fromLabel} → ${toLabel}`)) return;

    state.busy = true;
    $('loading').classList.add('show');
    $('btnToGreen').disabled = true;
    $('btnToBlue').disabled = true;
    try {
      const path = target === 'green' ? '/api/traffic/to-green' : '/api/traffic/to-blue';
      const st = await api(path, {
        method: 'POST',
        body: JSON.stringify({ actor, reason }),
      });
      state.traffic = st;
      renderTrafficUI();
      await loadTrafficHistory();
      const label = st.active === 'green' ? '绿环境' : st.active === 'blue' ? '蓝环境' : '目标环境';
      toast(`切流成功，当前生产：${label}`);
      $('footerTraffic').textContent = st.active === 'green' ? '生产流量：绿' : st.active === 'blue' ? '生产流量：蓝（绿环境仅预发）' : '生产流量：未知';
    } catch (err) {
      toast(err.message || String(err), 'error');
      await loadTraffic();
    } finally {
      state.busy = false;
      $('loading').classList.remove('show');
      renderTrafficUI();
    }
  }

  async function loadList() {
    const list = filterByTarget(await api('/api/releases'), 'green');
    const el = $('list');
    if (!list.length) {
      el.innerHTML = '<div class="empty">还没有历史记录</div>';
      return;
    }
    el.innerHTML = list.map((r) => `
      <button type="button" class="release-item ${state.current?.id === r.id ? 'active' : ''}" data-id="${r.id}">
        <strong>${escapeHtml(r.title)}</strong>
        <span class="meta">${escapeHtml(r.status)} · ${new Date(r.updated_at).toLocaleString()}</span>
      </button>`).join('');
    el.querySelectorAll('.release-item').forEach((node) => {
      node.addEventListener('click', () => select(node.dataset.id));
    });
  }

  async function select(id) {
    state.current = await api(`/api/releases/${id}`);
    renderUI();
    renderLogs(state.current);
    await loadList();
    schedulePoll();
    toast('已切换到：' + state.current.title);
  }

  function resetDeploy() {
    if (isAnyDeployBusy()) {
      toast('有部署任务进行中，请等待完成后再新建', 'error');
      return;
    }
    if (state.pollTimer) {
      clearInterval(state.pollTimer);
      state.pollTimer = null;
    }
    state.current = null;
    $('title').value = '';
    $('logFold').open = false;
    const resetBtn = $('btnNewDeploy');
    if (resetBtn) resetBtn.textContent = '再部署一遍 →';
    renderUI();
    renderLogs(null);
    loadList();
    toast('已重置，请填写新的发布名称开始部署');
  }

  async function autoPickRelease(list) {
    const greenList = filterByTarget(list, 'green');
    const active = greenList.find((r) => {
      const step = currentStep(r);
      return step > 0 || isDeployInProgress(r);
    });
    if (active) await select(active.id);
  }

  async function createRelease() {
    const title = $('title').value.trim();
    if (!title) throw new Error('请填写发布名称');
    const author = $('author').value.trim();
    if (!author) throw new Error('请填写你的名字');
    const rel = await api('/api/releases', {
      method: 'POST',
      body: JSON.stringify({
        title,
        commit_sha: 'green-deploy',
        author,
        level: 'normal',
        repo: 'juege-osh/osh',
        items: [{
          title: '前后端绿环境部署',
          type: 'code',
          ref: 'deploy-149',
          developer: $('developer').value,
          expected_impact: $('impact').value,
          reviewer1: $('rev1').value,
          reviewer2: $('rev2').value,
        }],
      }),
    });
    state.current = rel;
    toast('第 1 步完成！继续点绿色按钮');
  }

  async function submitReview() {
    const actor = $('author').value.trim() || 'ops';
    state.current = await api(`/api/releases/${state.current.id}/submit-review`, {
      method: 'POST',
      body: JSON.stringify({ actor }),
    });
    toast('第 2 步完成！继续点绿色按钮');
  }

  async function completeApproval() {
    const item = state.current.items[0];
    if (!reviewsOK(item)) {
      for (const reviewer of [item.reviewer1, item.reviewer2]) {
        await api(`/api/items/${item.id}/reviews`, {
          method: 'POST',
          body: JSON.stringify({
            reviewer, tested: true,
            demo_seen: reviewer !== item.developer,
            result: 'approve', comment: '通过',
          }),
        });
      }
    }
    state.current = await api(`/api/releases/${state.current.id}`);
    if (isBoss()) {
      state.current = await api(`/api/releases/${state.current.id}/boss-approve`, {
        method: 'POST',
        body: JSON.stringify({ reviewer: state.user.username, comment: '终审通过' }),
      });
      toast('终审通过，可以部署了');
    } else {
      toast('双评审已提交，等待 juege 终审');
    }
  }

  async function bossApproveOnly() {
    state.current = await api(`/api/releases/${state.current.id}/boss-approve`, {
      method: 'POST',
      body: JSON.stringify({ reviewer: state.user.username, comment: '终审通过' }),
    });
    toast('终审通过，可以部署了');
  }

  async function deployGreen() {
    if (isOtherDeployBusy(state.current)) {
      throw new Error(`发布单「${state.activeDeploy.title}」正在部署中，请等待完成`);
    }
    const actor = $('author').value.trim() || 'ops';
    state.current = { ...state.current, status: 'deploying' };
    state.activeDeploy = {
      busy: true,
      id: state.current.id,
      title: state.current.title,
      status: 'deploying',
    };
    renderUI();
    renderDeployLock();

    const rel = await api(`/api/releases/${state.current.id}/deploy`, {
      method: 'POST',
      body: JSON.stringify({ actor }),
    });
    state.current = rel;
    toast('已触发部署，正在等待 GitHub Actions…');
    $('logFold').open = true;
    await loadActiveDeploy();
    schedulePoll();
  }

  async function handleMainAction() {
    if (state.busy) return;
    state.busy = true;
    $('loading').classList.add('show');
    $('mainBtn').disabled = true;
    try {
      const step = currentStep(state.current);
      if (step === 1) await createRelease();
      else if (step === 2) await submitReview();
      else if (step === 3) {
        if (reviewsOK(state.current.items?.[0]) && isBoss()) await bossApproveOnly();
        else await completeApproval();
      }
      else if (step === 4) {
        $('loading').classList.remove('show');
        await deployGreen();
      }
      renderUI();
      renderLogs(state.current);
      await loadList();
      if (currentStep(state.current) > 1 && currentStep(state.current) < 5) {
        $('logFold').open = true;
      }
    } catch (err) {
      toast(err.message || String(err), 'error');
    } finally {
      state.busy = false;
      $('loading').classList.remove('show');
      $('mainBtn').disabled = false;
    }
  }

  function schedulePoll() {
    if (state.pollTimer) clearInterval(state.pollTimer);
    state.pollTimer = null;
    if (!state.current) return;
    const step = currentStep(state.current);
    if (!isDeployInProgress(state.current) && step !== 4) return;
    state.pollTimer = setInterval(async () => {
      try {
        if (!state.current) return;
        state.current = await api(`/api/releases/${state.current.id}`);
        renderUI();
        renderLogs(state.current);
        await loadList();
        await loadActiveDeploy();
        if (!isDeployInProgress(state.current)) {
          clearInterval(state.pollTimer);
          state.pollTimer = null;
          const step = currentStep(state.current);
          if (step === 0) toast('部署完成！请打开绿环境验收');
          else if (state.current.status === 'failed') toast('部署失败，请查看日志', 'error');
        }
      } catch { /* ignore */ }
    }, 3000);
  }

  async function bootApp() {
    renderUI();
    renderBlueDeployUI();
    renderBlueLogs(null);
    try {
      await loadHealth();
      await loadTraffic();
      await loadActiveDeploy();
      await loadSqlTemplates();
      await loadDeploySnapshots();
      const list = await api('/api/releases');
      await loadList();
      await loadBlueList();
      await autoPickRelease(list);
      await autoPickBlueRelease(list);
      if (!state.current) renderUI();
      if (!state.currentBlue) renderBlueDeployUI();
      if (state.current && (isDeployInProgress(state.current) || state.current.status === 'deploying')) {
        await loadActiveDeploy();
        schedulePoll();
      }
      if (state.currentBlue && (isDeployInProgress(state.currentBlue) || state.currentBlue.status === 'deploying')) {
        await loadActiveDeploy();
        scheduleBlueDeployPoll();
      }
    } catch (err) {
      $('modeBadge').textContent = '未连接';
      $('modeBadge').className = 'badge offline';
      toast('无法连接服务，请先运行 go run ./cmd/server', 'error');
    }
  }

  async function boot() {
    initSidebar();
    initPageFromHash();
    $('btnLogin')?.addEventListener('click', login);
    $('loginPass')?.addEventListener('keydown', (e) => { if (e.key === 'Enter') login(); });
    $('btnLogout')?.addEventListener('click', logout);
    $('btnRollbackGreen')?.addEventListener('click', () => rollbackDeploy('green'));
    $('btnRollbackBlue')?.addEventListener('click', () => rollbackDeploy('blue'));
    $('mainBtn').addEventListener('click', handleMainAction);
    $('btnNewDeploy')?.addEventListener('click', resetDeploy);
    $('blueMainBtn')?.addEventListener('click', handleBlueMainAction);
    $('btnNewBlueDeploy')?.addEventListener('click', resetBlueDeploy);
    $('btnExecSql')?.addEventListener('click', executeCustomSql);
    $('btnExecBlueSql')?.addEventListener('click', executeBlueCustomSql);
    $('btnLoadTemplate')?.addEventListener('click', loadTemplateIntoEditor);
    $('btnBlueLoadTemplate')?.addEventListener('click', loadBlueTemplateIntoEditor);
    $('btnToGreen')?.addEventListener('click', () => switchTraffic('green'));
    $('btnToBlue')?.addEventListener('click', () => switchTraffic('blue'));
    $('btnSyncBlue')?.addEventListener('click', syncBlue);
    if (await restoreSession()) {
      await bootApp();
    }
  }

  boot();
})();
