(() => {
  'use strict';

  const $ = (id) => document.getElementById(id);
  const state = { health: null, current: null, pollTimer: null, busy: false, page: 'deploy', activeDeploy: null, activePollTimer: null };

  const PAGES = {
    deploy: {
      title: '部署绿环境',
      subtitle: '4 步向导 · GitHub Actions · 不影响蓝环境生产',
    },
    sql: {
      title: '更新绿环境数据库',
      subtitle: '自定义 SQL · 仅 osh-g-mysql · 与部署独立',
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
    document.querySelectorAll('.page').forEach((el) => {
      const active = el.dataset.page === page;
      el.classList.toggle('active', active);
      el.hidden = !active;
    });

    $('pageTitle').textContent = PAGES[page].title;
    $('pageSubtitle').textContent = PAGES[page].subtitle;
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
    const hash = (location.hash || '').replace('#', '');
    navigate(PAGES[hash] ? hash : (localStorage.getItem('osh_page') || 'deploy'));
  }

  function isOtherDeployBusy() {
    return state.activeDeploy?.busy && state.activeDeploy.id !== state.current?.id;
  }

  function isAnyDeployBusy() {
    return !!state.activeDeploy?.busy;
  }

  async function loadActiveDeploy() {
    try {
      const d = await api('/api/deploy/active');
      state.activeDeploy = d.busy ? d : null;
    } catch {
      state.activeDeploy = null;
    }
    renderDeployLock();
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
    const isOther = isOtherDeployBusy();
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
      }
    }, 4000);
  }

  async function api(path, opts = {}) {
    const res = await fetch(path, { headers: { 'Content-Type': 'application/json' }, ...opts });
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
    if (rel.status === 'draft') return 2;
    if (rel.status === 'reviewing') return 3;
    return 2;
  }

  function renderStepper(step) {
    document.querySelectorAll('.stepper-item').forEach((el) => {
      const n = Number(el.dataset.step);
      el.classList.remove('active', 'done');
      if (step === 0 || (step > 0 && n < step)) el.classList.add('done');
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
    const cfg = STEPS[step] || STEPS[4];
    const inProgress = isDeployInProgress(rel);

    renderStepper(step === 0 ? 4 : step);

    $('actionForm').style.display = step === 1 && cfg.showForm ? 'block' : 'none';
    $('successBox').hidden = step !== 0;
    $('waitingBox').hidden = !inProgress;
    $('mainBtn').hidden = step === 0 || step === -1 || inProgress || (step === 4 && isOtherDeployBusy());
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

    if (step === 4 && isOtherDeployBusy() && !inProgress) {
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

    $('stepBadge').textContent = cfg.badge;
    $('actionTitle').textContent = cfg.title;
    $('actionDesc').textContent = cfg.desc;
    $('mainBtnText').textContent = cfg.btn;
    renderDeployLock();
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
    $('footerBackend').textContent = d.backend_ref || '—';
    $('footerFrontend').textContent = d.frontend_ref || '—';

    const tip = $('sqlTip');
    if (tip) {
      tip.textContent = mysql.configured
        ? `目标：${mysql.green_container || 'osh-g-mysql'} / ${mysql.database || 'backstage'}`
        : '请在 config.env 配置 GREEN_MYSQL_ROOT_PASSWORD 后才能执行 SQL。';
    }
  }

  async function loadSqlTemplates() {
    const sel = $('sqlTemplate');
    if (!sel) return;
    try {
      const list = await api('/api/migrations');
      sel.innerHTML = '<option value="">从模板填入（可选）</option>' +
        list.map((m) => `<option value="${escapeHtml(m.id)}">${escapeHtml(m.description || m.name)}</option>`).join('');
    } catch {
      sel.innerHTML = '<option value="">无可用模板</option>';
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
    try {
      const t = await api('/api/traffic/status');
      const isGreen = (t.output || '').includes('active (by :80): green');
      $('footerTraffic').textContent = isGreen ? '生产流量：绿' : '生产流量：蓝（绿环境仅预发）';
    } catch {
      $('footerTraffic').textContent = '';
    }
  }

  async function loadList() {
    const list = await api('/api/releases');
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
    const active = list.find((r) => {
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
    const boss = state.health?.deploy?.boss_reviewer || '觉哥';
    state.current = await api(`/api/releases/${state.current.id}/boss-approve`, {
      method: 'POST',
      body: JSON.stringify({ reviewer: boss, comment: '终审通过' }),
    });
    toast('第 3 步完成！可以部署了');
  }

  async function deployGreen() {
    if (isOtherDeployBusy()) {
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
      else if (step === 3) await completeApproval();
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

  async function boot() {
    initSidebar();
    initPageFromHash();
    $('mainBtn').addEventListener('click', handleMainAction);
    $('btnNewDeploy')?.addEventListener('click', resetDeploy);
    $('btnExecSql')?.addEventListener('click', executeCustomSql);
    $('btnLoadTemplate')?.addEventListener('click', loadTemplateIntoEditor);
    renderUI();
    renderLogs(null);
    try {
      await loadHealth();
      await loadTraffic();
      await loadActiveDeploy();
      await loadSqlTemplates();
      const list = await api('/api/releases');
      await loadList();
      await autoPickRelease(list);
      if (!state.current) renderUI();
      if (state.current && (isDeployInProgress(state.current) || state.current.status === 'deploying')) {
        await loadActiveDeploy();
        schedulePoll();
      }
    } catch (err) {
      $('modeBadge').textContent = '未连接';
      $('modeBadge').className = 'badge offline';
      toast('无法连接服务，请先运行 go run ./cmd/server', 'error');
    }
  }

  boot();
})();
