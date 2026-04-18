'use strict';

// ── State ─────────────────────────────────────────────────────────────────────
let currentScenario = null;
let currentResult   = null;
let mcResult        = null;

// ── Boot ──────────────────────────────────────────────────────────────────────
document.addEventListener('DOMContentLoaded', async () => {
  setupTabs();
  await loadDefault();
});

// ── Tab switching ─────────────────────────────────────────────────────────────
function setupTabs() {
  document.querySelectorAll('.tab-btn').forEach(btn => {
    btn.addEventListener('click', () => switchTab(btn.dataset.tab));
  });
}

function switchTab(name) {
  document.querySelectorAll('.tab-btn').forEach(b =>
    b.classList.toggle('active', b.dataset.tab === name));
  document.querySelectorAll('.tab-panel').forEach(p =>
    p.classList.toggle('hidden', p.dataset.panel !== name));

  if (name === 'montecarlo' && currentResult && !mcResult) runMonteCarlo();
}

// ── Load default scenario and auto-run ───────────────────────────────────────
async function loadDefault() {
  setLoading(true);
  try {
    const res = await fetch('/api/default-scenario');
    if (!res.ok) throw new Error('scenario fetch failed');
    currentScenario = await res.json();
    document.getElementById('scenario-json').value =
      JSON.stringify(currentScenario, null, 2);
    await runPlan();
  } catch (e) {
    showError('Could not load default scenario: ' + e.message);
  } finally {
    setLoading(false);
  }
}

// ── Run plan ──────────────────────────────────────────────────────────────────
document.getElementById('run-btn').addEventListener('click', async () => {
  const raw = document.getElementById('scenario-json').value;
  try {
    currentScenario = JSON.parse(raw);
  } catch {
    showError('Invalid JSON in scenario editor');
    return;
  }
  mcResult = null;
  setLoading(true);
  try {
    await runPlan();
  } finally {
    setLoading(false);
  }
});

async function runPlan() {
  const res = await fetch('/api/plan', {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(currentScenario),
  });
  if (!res.ok) throw new Error(await res.text());
  currentResult = await res.json();
  renderAll();
}

// ── Render all panels ─────────────────────────────────────────────────────────
function renderAll() {
  renderSummary();
  renderGoals();
  renderNetWorth();
  renderCashFlow();
  // Monte Carlo rendered on demand
}

// ── Summary bar ───────────────────────────────────────────────────────────────
function renderSummary() {
  const r = currentResult;
  document.getElementById('summary-bar').innerHTML = `
    <div class="summary-card">
      <div class="text-xs text-gray-400 mb-1">Retirement SIP needed</div>
      <div class="text-xl font-bold text-indigo-300">${fmt(r.retirementSip)}<span class="text-sm font-normal text-gray-400">/mo</span></div>
    </div>
    <div class="summary-card">
      <div class="text-xs text-gray-400 mb-1">Goals funded</div>
      <div class="text-xl font-bold text-green-400">${r.goals.filter(g => g.tag !== 'UNAFFORDABLE').length} / ${r.goals.length}</div>
    </div>
    <div class="summary-card">
      <div class="text-xs text-gray-400 mb-1">Current portfolio</div>
      <div class="text-xl font-bold text-white">${fmtCr(currentScenario.currentPortfolioValue)}</div>
    </div>
    <div class="summary-card">
      <div class="text-xs text-gray-400 mb-1">Monthly surplus</div>
      <div class="text-xl font-bold text-blue-300">${fmt(currentScenario.monthlyIncome - currentScenario.monthlyExpense)}<span class="text-sm font-normal text-gray-400">/mo</span></div>
    </div>
  `;
}

// ── Goals tab ─────────────────────────────────────────────────────────────────
function renderGoals() {
  const goals = currentResult.goals || [];
  const sips  = currentResult.recommendedSipSchedule || [];

  const tagClass = tag => ({
    COMFORTABLE:  'comfortable',
    MANAGEABLE:   'manageable',
    TIGHT:        'tight',
    UNAFFORDABLE: 'unaffordable',
  }[tag] || 'manageable');

  const goalCards = goals.map(g => {
    const tc   = tagClass(g.tag);
    const pct  = g.targetAmount > 0
      ? Math.min(100, (g.projectedAmount / g.targetAmount) * 100)
      : 100;
    return `
      <div class="goal-card">
        <div class="flex items-start justify-between mb-2">
          <div>
            <div class="font-semibold text-sm">${g.name}</div>
            <div class="text-xs text-gray-400 mt-0.5">${g.targetDate || ''}</div>
          </div>
          <span class="badge badge-${tc}">${g.tag}</span>
        </div>
        <div class="flex justify-between text-xs text-gray-400 mt-3">
          <span>Projected</span><span class="text-white font-medium">${fmtCr(g.projectedAmount)}</span>
        </div>
        <div class="flex justify-between text-xs text-gray-400 mt-1">
          <span>Target</span><span>${g.targetAmount > 0 ? fmtCr(g.taxTotal || g.targetAmount) : '—'}</span>
        </div>
        ${g.shortfallAmount > 0 ? `
        <div class="flex justify-between text-xs mt-1">
          <span class="text-red-400">Shortfall</span><span class="text-red-300 font-medium">${fmtCr(g.shortfallAmount)}</span>
        </div>` : ''}
        <div class="progress-track">
          <div class="progress-fill progress-${tc}" style="width:${pct.toFixed(1)}%"></div>
        </div>
        <div class="text-right text-xs text-gray-500 mt-1">${pct.toFixed(0)}% funded</div>
      </div>`;
  }).join('');

  document.getElementById('goal-cards').innerHTML = goalCards || '<p class="text-gray-500">No goals returned.</p>';

  // SIP schedule table
  if (sips.length > 0) {
    const rows = sips.map(s =>
      `<tr><td>${s.year}</td><td class="text-right font-medium text-indigo-300">${fmt(s.amount)}</td></tr>`
    ).join('');
    document.getElementById('sip-schedule').innerHTML = `
      <table class="sip-table w-full">
        <thead><tr><th class="text-left">Year</th><th class="text-right">Monthly SIP</th></tr></thead>
        <tbody>${rows}</tbody>
      </table>`;
  } else {
    document.getElementById('sip-schedule').innerHTML = '<p class="text-gray-500 text-sm">No SIP schedule.</p>';
  }
}

// ── Net Worth tab (stacked bar chart) ─────────────────────────────────────────
function renderNetWorth() {
  const s = currentScenario;
  if (!s) return;

  // Build a simple year-by-year projection from the portfolio breakdown
  const today = new Date();
  const retireYear = (new Date(s.dob).getFullYear()) + s.retirementAge;
  const endYear    = (new Date(s.dob).getFullYear()) + s.lifeExpectancy;
  const startYear  = today.getFullYear();

  const years   = [];
  const equity  = [];
  const debt    = [];
  const liquid  = [];

  let eq = (s.portfolioBreakup?.Equity?.Amount || 0);
  let db = (s.portfolioBreakup?.Debt?.Amount   || 0);
  let lq = (s.portfolioBreakup?.Liquid?.Amount || 0);

  const incomeGrowth  = (s.incomeParams?.[0]?.value  || 8) / 100;
  const expenseGrowth = (s.expenseParams?.[0]?.value || 6) / 100;

  let income  = s.monthlyIncome  * 12;
  let expense = s.monthlyExpense * 12;
  const totalSIP = (s.sips || []).reduce((a, b) => a + b.amount, 0) * 12;

  for (let y = startYear; y <= endYear; y++) {
    years.push(y);
    equity.push(eq / 1e7);
    debt.push(db / 1e7);
    liquid.push(lq / 1e7);

    const isRetired = y >= retireYear;
    const surplus   = isRetired ? 0 : Math.max(0, income - expense - totalSIP);

    // Grow each bucket
    eq = eq * 1.12 + (isRetired ? 0 : surplus * 0.70);
    db = db * 1.08 + (isRetired ? 0 : surplus * 0.20);
    lq = lq * 1.06 + (isRetired ? 0 : surplus * 0.10);

    // Retirement drawdown (rough)
    if (isRetired) {
      const draw = expense;
      const total = eq + db + lq;
      if (total > 0) {
        eq = Math.max(0, eq - draw * eq / total);
        db = Math.max(0, db - draw * db / total);
        lq = Math.max(0, lq - draw * lq / total);
      }
    }

    income  *= (1 + incomeGrowth);
    expense *= (1 + expenseGrowth);
  }

  Plotly.newPlot('nw-chart', [
    { x: years, y: equity, name: 'Equity', type: 'bar', marker: { color: '#6366f1' } },
    { x: years, y: debt,   name: 'Debt',   type: 'bar', marker: { color: '#22d3ee' } },
    { x: years, y: liquid, name: 'Liquid', type: 'bar', marker: { color: '#34d399' } },
  ], {
    barmode: 'stack',
    paper_bgcolor: 'transparent',
    plot_bgcolor:  'transparent',
    font: { color: '#e2e2f0', size: 11 },
    xaxis: { gridcolor: '#3b3b52', title: 'Year' },
    yaxis: { gridcolor: '#3b3b52', title: 'Net Worth (₹ Cr)' },
    legend: { bgcolor: 'transparent' },
    shapes: [{
      type: 'line', x0: retireYear, x1: retireYear, y0: 0, y1: 1,
      xref: 'x', yref: 'paper',
      line: { color: '#fb923c', width: 1.5, dash: 'dot' },
    }],
    annotations: [{
      x: retireYear, y: 1, xref: 'x', yref: 'paper',
      text: 'Retirement', showarrow: false,
      font: { color: '#fb923c', size: 10 }, yanchor: 'bottom',
    }],
    margin: { t: 20, r: 20, b: 50, l: 60 },
  }, { responsive: true, displayModeBar: false });
}

// ── Cash Flow tab (simple income vs expense waterfall) ────────────────────────
function renderCashFlow() {
  const s = currentScenario;
  if (!s) return;

  const today    = new Date();
  const retireYear = (new Date(s.dob).getFullYear()) + s.retirementAge;
  const startYear  = today.getFullYear();
  const endYear    = retireYear + 5;

  const years   = [];
  const incomes = [];
  const exps    = [];
  const surplus = [];

  let inc = s.monthlyIncome  * 12;
  let exp = s.monthlyExpense * 12;
  const incGrowth = (s.incomeParams?.[0]?.value  || 8) / 100;
  const expGrowth = (s.expenseParams?.[0]?.value || 6) / 100;
  const totalLoan = (s.loans || []).reduce((a, l) => a + (l.emi || 0), 0) * 12;
  const totalSIP  = (s.sips  || []).reduce((a, b) => a + b.amount, 0) * 12;

  for (let y = startYear; y <= endYear; y++) {
    const isRetired = y >= retireYear;
    years.push(y);
    incomes.push(isRetired ? 0 : inc / 1e5);
    const totalExp = isRetired ? exp : exp + totalLoan + totalSIP;
    exps.push(totalExp / 1e5);
    surplus.push(isRetired ? -(exp / 1e5) : Math.max(0, inc - totalExp) / 1e5);

    inc *= (1 + incGrowth);
    exp *= (1 + expGrowth);
  }

  Plotly.newPlot('cf-chart', [
    { x: years, y: incomes, name: 'Income',   type: 'bar', marker: { color: '#4ade80' } },
    { x: years, y: exps,    name: 'Expenses + EMI + SIP', type: 'bar', marker: { color: '#f87171' } },
    { x: years, y: surplus, name: 'Surplus',  type: 'scatter', mode: 'lines+markers',
      line: { color: '#818cf8', width: 2 }, marker: { size: 4 } },
  ], {
    barmode: 'group',
    paper_bgcolor: 'transparent',
    plot_bgcolor:  'transparent',
    font: { color: '#e2e2f0', size: 11 },
    xaxis: { gridcolor: '#3b3b52', title: 'Year' },
    yaxis: { gridcolor: '#3b3b52', title: 'Annual (₹ Lakh)' },
    legend: { bgcolor: 'transparent' },
    margin: { t: 20, r: 20, b: 50, l: 60 },
  }, { responsive: true, displayModeBar: false });
}

// ── Monte Carlo tab ───────────────────────────────────────────────────────────
async function runMonteCarlo() {
  document.getElementById('mc-loading').classList.remove('hidden');
  document.getElementById('mc-content').classList.add('hidden');
  try {
    const res = await fetch('/api/monte-carlo', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ params: currentScenario, runs: 300 }),
    });
    if (!res.ok) throw new Error(await res.text());
    mcResult = await res.json();
    renderMonteCarlo();
  } catch (e) {
    showError('Monte Carlo failed: ' + e.message);
  } finally {
    document.getElementById('mc-loading').classList.add('hidden');
    document.getElementById('mc-content').classList.remove('hidden');
  }
}

function renderMonteCarlo() {
  if (!mcResult) return;

  // Goal probability bars
  const probs = mcResult.goalProbabilities || [];
  document.getElementById('mc-probs').innerHTML = probs.map(gp => `
    <div class="mb-4">
      <div class="flex justify-between text-sm mb-1">
        <span class="font-medium">${gp.name}</span>
        <span class="text-indigo-300 font-bold">${(gp.successProbability * 100).toFixed(0)}%</span>
      </div>
      <div class="prob-bar-track">
        <div class="prob-bar-fill" style="width:${(gp.successProbability * 100).toFixed(1)}%"></div>
      </div>
      <div class="flex justify-between text-xs text-gray-500 mt-1">
        <span>P10: ${fmtCr(gp.p10Projected)}</span>
        <span>P50: ${fmtCr(gp.medianProjected)}</span>
        <span>P90: ${fmtCr(gp.p90Projected)}</span>
      </div>
    </div>`).join('');

  // P10/P50/P90 corpus chart (bar chart across goals)
  const names = probs.map(g => g.name);
  Plotly.newPlot('mc-chart', [
    { x: names, y: probs.map(g => g.p10Projected / 1e7),    name: 'P10 (pessimistic)', type: 'bar', marker: { color: '#f87171' } },
    { x: names, y: probs.map(g => g.medianProjected / 1e7), name: 'P50 (median)',      type: 'bar', marker: { color: '#818cf8' } },
    { x: names, y: probs.map(g => g.p90Projected / 1e7),    name: 'P90 (optimistic)',  type: 'bar', marker: { color: '#4ade80' } },
  ], {
    barmode: 'group',
    paper_bgcolor: 'transparent',
    plot_bgcolor:  'transparent',
    font: { color: '#e2e2f0', size: 11 },
    xaxis: { gridcolor: '#3b3b52' },
    yaxis: { gridcolor: '#3b3b52', title: 'Projected (₹ Cr)' },
    legend: { bgcolor: 'transparent' },
    margin: { t: 20, r: 20, b: 80, l: 60 },
  }, { responsive: true, displayModeBar: false });

  document.getElementById('mc-runs').textContent = `Based on ${mcResult.runs} simulations`;
}

// ── Helpers ───────────────────────────────────────────────────────────────────
function fmt(n) {
  if (!n && n !== 0) return '—';
  if (n >= 1e7) return '₹' + (n / 1e7).toFixed(2) + ' Cr';
  if (n >= 1e5) return '₹' + (n / 1e5).toFixed(1) + ' L';
  return '₹' + Math.round(n).toLocaleString('en-IN');
}

function fmtCr(n) {
  if (!n && n !== 0) return '—';
  if (n === 0) return '₹0';
  if (n >= 1e7) return '₹' + (n / 1e7).toFixed(2) + ' Cr';
  if (n >= 1e5) return '₹' + (n / 1e5).toFixed(1) + ' L';
  return '₹' + Math.round(n).toLocaleString('en-IN');
}

function setLoading(on) {
  document.getElementById('loading-overlay').classList.toggle('hidden', !on);
}

function showError(msg) {
  const el = document.getElementById('error-banner');
  el.textContent = msg;
  el.classList.remove('hidden');
  setTimeout(() => el.classList.add('hidden'), 5000);
}
