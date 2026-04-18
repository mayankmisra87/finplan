'use strict';

// ── State ─────────────────────────────────────────────────────────────────────
let currentScenario  = null;
let currentResult    = null;
let projectionResult = null;
let cashflowResult   = null;
let mcResult         = null;

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
  mcResult         = null;
  projectionResult = null;
  cashflowResult   = null;
  setLoading(true);
  try {
    await runPlan();
  } finally {
    setLoading(false);
  }
});

async function runPlan() {
  const body = JSON.stringify(currentScenario);
  const [planRes, projRes, cfRes] = await Promise.all([
    fetch('/api/plan',       { method: 'POST', headers: { 'Content-Type': 'application/json' }, body }),
    fetch('/api/projection', { method: 'POST', headers: { 'Content-Type': 'application/json' }, body }),
    fetch('/api/cashflow',   { method: 'POST', headers: { 'Content-Type': 'application/json' }, body }),
  ]);
  if (!planRes.ok) throw new Error(await planRes.text());
  if (!projRes.ok) throw new Error(await projRes.text());
  if (!cfRes.ok)   throw new Error(await cfRes.text());
  currentResult    = await planRes.json();
  projectionResult = await projRes.json();
  cashflowResult   = await cfRes.json();
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
      <div class="text-xl font-bold text-blue-300">${fmt((currentScenario.monthlyIncome + (currentScenario.spouse?.monthlyIncome || 0)) - currentScenario.monthlyExpense)}<span class="text-sm font-normal text-gray-400">/mo</span></div>
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

// ── Net Worth tab — engine-backed projection ───────────────────────────────────
function renderNetWorth() {
  if (!projectionResult) return;

  const snaps = projectionResult.snapshots;
  const primaryRetireYear = projectionResult.primaryRetireYear;
  const spouseRetireYear  = projectionResult.spouseRetireYear || null;

  const years  = snaps.map(s => s.year);
  const equity = snaps.map(s => s.equity / 1e7);
  const debt   = snaps.map(s => s.debt   / 1e7);
  const liquid = snaps.map(s => s.liquid / 1e7);

  // Retirement marker shapes + annotations
  const shapes = [{
    type: 'line', x0: primaryRetireYear, x1: primaryRetireYear, y0: 0, y1: 1,
    xref: 'x', yref: 'paper', line: { color: '#fb923c', width: 1.5, dash: 'dot' },
  }];
  const annotations = [{
    x: primaryRetireYear, y: 1, xref: 'x', yref: 'paper',
    text: currentScenario?.spouse ? 'Arjun retires' : 'Retirement',
    showarrow: false, font: { color: '#fb923c', size: 10 }, yanchor: 'bottom',
  }];
  if (spouseRetireYear && spouseRetireYear !== primaryRetireYear) {
    shapes.push({
      type: 'line', x0: spouseRetireYear, x1: spouseRetireYear, y0: 0, y1: 1,
      xref: 'x', yref: 'paper', line: { color: '#a78bfa', width: 1.5, dash: 'dot' },
    });
    annotations.push({
      x: spouseRetireYear, y: 0.92, xref: 'x', yref: 'paper',
      text: 'Priya retires', showarrow: false,
      font: { color: '#a78bfa', size: 10 }, yanchor: 'bottom',
    });
  }

  Plotly.newPlot('nw-chart', [
    { x: years, y: equity, name: 'Equity', type: 'bar', marker: { color: '#6366f1' } },
    { x: years, y: debt,   name: 'Debt',   type: 'bar', marker: { color: '#22d3ee' } },
    { x: years, y: liquid, name: 'Liquid', type: 'bar', marker: { color: '#34d399' } },
  ], {
    barmode: 'stack',
    paper_bgcolor: 'transparent', plot_bgcolor: 'transparent',
    font: { color: '#e2e2f0', size: 11 },
    xaxis: { gridcolor: '#3b3b52', title: 'Year' },
    yaxis: { gridcolor: '#3b3b52', title: 'Net Worth (₹ Cr)' },
    legend: { bgcolor: 'transparent' },
    shapes, annotations,
    margin: { t: 20, r: 20, b: 50, l: 60 },
  }, { responsive: true, displayModeBar: false });
}

// ── Cash Flow tab — engine-backed Sankey with year slider ─────────────────────
function renderCashFlow() {
  if (!cashflowResult) return;
  const yrs    = cashflowResult.years;
  if (!yrs.length) return;

  const slider = document.getElementById('cf-year-slider');
  const startY = yrs[0].year;
  const endY   = yrs[yrs.length - 1].year;
  slider.min   = startY;
  slider.max   = endY;
  if (+slider.value < startY || +slider.value > endY) slider.value = startY;

  slider.oninput = () => renderSankeyYear(+slider.value);
  renderSankeyYear(+slider.value);
}

function renderSankeyYear(year) {
  const cfMap = {};
  cashflowResult.years.forEach(y => { cfMap[y.year] = y; });
  const cf = cfMap[year];
  if (!cf) return;

  document.getElementById('cf-year-label').textContent = year;

  const primaryRetired = year >= cashflowResult.primaryRetireYear;
  const spouseRetired  = cashflowResult.spouseRetireYear
    ? year >= cashflowResult.spouseRetireYear : true;
  let status = '';
  if (primaryRetired && spouseRetired)
    status = 'Both retired — portfolio drawdown phase';
  else if (primaryRetired)
    status = 'Primary earner retired';
  else if (spouseRetired && cashflowResult.spouseRetireYear)
    status = 'Spouse retired';
  document.getElementById('cf-year-status').textContent = status;

  const { nodeLabels, nodeColors, sources, targets, values, linkColors } =
    buildSankeyData(cf);

  Plotly.react('cf-chart', [{
    type: 'sankey',
    orientation: 'h',
    arrangement: 'snap',
    node: {
      pad: 18, thickness: 22,
      line: { color: '#1a1a2e', width: 0.5 },
      label: nodeLabels,
      color: nodeColors,
    },
    link: {
      source: sources,
      target: targets,
      value:  values,
      color:  linkColors,
      customdata: values.map(v => fmtCr(v)),
      hovertemplate: '%{source.label} → %{target.label}<br>%{customdata}<extra></extra>',
    },
  }], {
    paper_bgcolor: 'transparent',
    font: { color: '#e2e2f0', size: 12 },
    margin: { t: 10, r: 30, b: 10, l: 30 },
  }, { responsive: true, displayModeBar: false });
}

function buildSankeyData(cf) {
  const nodeLabels = [], nodeColors = [];
  const sources = [], targets = [], values = [], linkColors = [];

  function addNode(label, color) {
    nodeLabels.push(label);
    nodeColors.push(color);
    return nodeLabels.length - 1;
  }

  function addLink(src, tgt, val, col) {
    if (src < 0 || tgt < 0 || val < 500) return;
    sources.push(src); targets.push(tgt);
    values.push(Math.round(val));
    linkColors.push(col);
  }

  const totalIncome = cf.primaryIncome + cf.spouseIncome;

  if (totalIncome > 0) {
    // ── Pre-retirement: income → expenses + investments ───────────────────────
    const investTotal = cf.toEquity + cf.toDebt + cf.toLiquid;
    const totalSinks  = cf.expenses + cf.loanEMIs + cf.goalCosts + investTotal;
    const deficit     = Math.max(0, totalSinks - totalIncome);

    const primIdx   = cf.primaryIncome > 0 ? addNode('Primary Income', '#22c55e') : -1;
    const spouseIdx = cf.spouseIncome  > 0 ? addNode('Spouse Income',  '#86efac') : -1;
    const drawIdx   = deficit > 500     ? addNode('Portfolio Draw', '#f43f5e') : -1;

    const expIdx  =                       addNode('Living Expenses', '#ef4444');
    const emiIdx  = cf.loanEMIs  > 500 ? addNode('Loan EMIs',       '#f97316') : -1;
    const goalIdx = cf.goalCosts > 500 ? addNode('Goal Costs',      '#eab308') : -1;
    const eqIdx   = cf.toEquity  > 500 ? addNode('→ Equity',        '#6366f1') : -1;
    const dbIdx   = cf.toDebt    > 500 ? addNode('→ Debt',          '#22d3ee') : -1;
    const lqIdx   = cf.toLiquid  > 500 ? addNode('→ Liquid',        '#34d399') : -1;

    // Each sink is split proportionally across income sources (and portfolio draw if deficit)
    const primShare   = totalSinks > 0 ? cf.primaryIncome / totalSinks : 0;
    const spouseShare = totalSinks > 0 ? cf.spouseIncome  / totalSinks : 0;
    const drawShare   = totalSinks > 0 ? deficit          / totalSinks : 0;

    const sinkDefs = [
      { idx: expIdx,  val: cf.expenses,  col: 'rgba(239,68,68,0.25)'  },
      { idx: emiIdx,  val: cf.loanEMIs,  col: 'rgba(249,115,22,0.25)' },
      { idx: goalIdx, val: cf.goalCosts, col: 'rgba(234,179,8,0.25)'  },
      { idx: eqIdx,   val: cf.toEquity,  col: 'rgba(99,102,241,0.3)'  },
      { idx: dbIdx,   val: cf.toDebt,    col: 'rgba(34,211,238,0.3)'  },
      { idx: lqIdx,   val: cf.toLiquid,  col: 'rgba(52,211,153,0.3)'  },
    ].filter(s => s.idx >= 0 && s.val > 500);

    for (const s of sinkDefs) {
      addLink(primIdx,   s.idx, s.val * primShare,   s.col);
      addLink(spouseIdx, s.idx, s.val * spouseShare, s.col);
      addLink(drawIdx,   s.idx, s.val * drawShare,   'rgba(244,63,94,0.25)');
    }
  } else {
    // ── Post-retirement: portfolio + annuity → expenses ───────────────────────
    const drawIdx  = addNode('Portfolio Drawdown', '#f43f5e');
    const annuIdx  = cf.npsAnnuity > 500 ? addNode('NPS Annuity', '#a78bfa') : -1;
    const expIdx   = addNode('Living Expenses',    '#ef4444');
    addLink(drawIdx, expIdx, cf.drawdown,   'rgba(244,63,94,0.3)');
    addLink(annuIdx, expIdx, cf.npsAnnuity, 'rgba(167,139,250,0.3)');
  }

  return { nodeLabels, nodeColors, sources, targets, values, linkColors };
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

  renderTrajectoryBands();

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

// ── Monte Carlo trajectory band chart ─────────────────────────────────────────
function renderTrajectoryBands() {
  const bands = mcResult?.trajectoryBands;
  if (!bands?.length) return;

  const years = bands.map(b => b.year);
  const p25   = bands.map(b => b.p25 / 1e7);
  const p50   = bands.map(b => b.p50 / 1e7);
  const p75   = bands.map(b => b.p75 / 1e7);

  // Overlay the deterministic projection as a reference line
  const det       = projectionResult?.snapshots || [];
  const detYears  = det.map(s => s.year);
  const detTotals = det.map(s => s.total / 1e7);

  const retYear    = projectionResult?.primaryRetireYear;
  const spouseYear = projectionResult?.spouseRetireYear;
  const shapes = [], annotations = [];
  if (retYear) {
    shapes.push({ type:'line', x0:retYear, x1:retYear, y0:0, y1:1,
      xref:'x', yref:'paper', line:{ color:'#fb923c', width:1, dash:'dot' } });
    annotations.push({ x:retYear, y:1, xref:'x', yref:'paper',
      text: currentScenario?.spouse ? 'Arjun retires' : 'Retirement',
      showarrow:false, font:{ color:'#fb923c', size:9 }, yanchor:'bottom' });
  }
  if (spouseYear && spouseYear !== retYear) {
    shapes.push({ type:'line', x0:spouseYear, x1:spouseYear, y0:0, y1:1,
      xref:'x', yref:'paper', line:{ color:'#a78bfa', width:1, dash:'dot' } });
    annotations.push({ x:spouseYear, y:0.9, xref:'x', yref:'paper',
      text:'Priya retires', showarrow:false,
      font:{ color:'#a78bfa', size:9 }, yanchor:'bottom' });
  }

  Plotly.newPlot('mc-trajectory-chart', [
    // Invisible lower bound — needed as fill baseline
    { x:years, y:p25, name:'P25', type:'scatter', mode:'lines',
      line:{ color:'transparent', width:0 }, showlegend:false, hoverinfo:'skip' },
    // Filled P25–P75 band
    { x:years, y:p75, name:'P25–P75 range', type:'scatter', mode:'lines',
      fill:'tonexty', fillcolor:'rgba(99,102,241,0.15)',
      line:{ color:'transparent', width:0 } },
    // P50 median
    { x:years, y:p50, name:'P50 median', type:'scatter', mode:'lines',
      line:{ color:'#818cf8', width:2 } },
    // Deterministic baseline
    { x:detYears, y:detTotals, name:'Deterministic', type:'scatter', mode:'lines',
      line:{ color:'#4ade80', width:1.5, dash:'dot' } },
  ], {
    paper_bgcolor:'transparent', plot_bgcolor:'transparent',
    font:{ color:'#e2e2f0', size:11 },
    xaxis:{ gridcolor:'#3b3b52', title:'Year' },
    yaxis:{ gridcolor:'#3b3b52', title:'Net Worth (₹ Cr)' },
    legend:{ bgcolor:'transparent' },
    shapes, annotations,
    margin:{ t:10, r:20, b:50, l:60 },
  }, { responsive:true, displayModeBar:false });
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
