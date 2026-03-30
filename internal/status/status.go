package status

import (
	"encoding/json"
	"fmt"
	"html/template"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"

	"hyperliquid/internal/inplay"
	"hyperliquid/internal/market"
)

type Snapshot struct {
	Generated time.Time         `json:"generated"`
	Exchange  string            `json:"exchange"`
	Active    []string          `json:"active"`
	Rows      []market.Scored   `json:"rows"`
	Conf      map[string]string `json:"conf"`
	InPlay    []inplay.Entry    `json:"inplay"`
	Engine    *EngineSnapshot   `json:"engine,omitempty"`
}

type EngineSnapshot struct {
	Mode       string           `json:"mode"`
	Paused     bool             `json:"paused"`
	Session    string           `json:"session"`
	LastSignal string           `json:"lastSignal"`
	UpdatedAt  time.Time        `json:"updatedAt"`
	ControlURL string           `json:"controlUrl"`
	StatusURL  string           `json:"statusUrl"`
	Account    EngineAccount    `json:"account"`
	Positions  []EnginePosition `json:"positions"`
}

type EngineAccount struct {
	Equity       float64 `json:"equity"`
	AvailableUSD float64 `json:"availableUsd"`
	ReservedUSD  float64 `json:"reservedUsd"`
	OpenCount    int     `json:"openCount"`
}

type EnginePosition struct {
	Symbol   string      `json:"symbol"`
	Side     market.Side `json:"side"`
	Size     float64     `json:"size"`
	Entry    float64     `json:"entry"`
	Mark     float64     `json:"mark"`
	Stop     float64     `json:"stop"`
	Target   float64     `json:"target"`
	PnL      float64     `json:"pnl"`
	Imported bool        `json:"imported"`
}

type Store struct {
	mu  sync.RWMutex
	cur Snapshot
}

func NewStore() *Store { return &Store{} }

func (s *Store) SetSnap(sn Snapshot) {
	s.mu.Lock()
	s.cur = sn
	s.mu.Unlock()
}

func (s *Store) Snapshot() Snapshot {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.cur
}

func (s *Store) Handler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		snap := s.Snapshot()
		rows := append([]market.Scored(nil), snap.Rows...)
		sort.Slice(rows, func(i, j int) bool { return rows[i].Score > rows[j].Score })
		payload := dashboardPayload{
			Generated: snap.Generated.Format(time.RFC3339),
			Exchange:  snap.Exchange,
			Active:    snap.Active,
			Rows:      make([]dashboardRow, 0, len(rows)),
			InPlay:    make([]dashboardInPlay, 0, len(snap.InPlay)),
			Engine:    snap.Engine,
		}
		inPlayBySymbol := make(map[string]inplay.Entry, len(snap.InPlay))
		for _, e := range snap.InPlay {
			inPlayBySymbol[e.Symbol] = e
			payload.InPlay = append(payload.InPlay, dashboardInPlay{
				Symbol:   market.DisplaySymbol(e.Symbol),
				Grade:    e.CurrentGrade,
				Score:    e.CurrentScore,
				Slope:    e.ScoreSlope,
				State:    string(e.State),
				Momentum: e.Momentum,
				AgeMin:   e.AgeMinutes,
				SideBias: strings.ToUpper(e.SideBias),
			})
		}
		side := inferDirection(snap.Exchange)
		for _, row := range rows {
			grade := row.Grade
			if snap.Conf != nil && snap.Conf[row.Symbol] != "" {
				grade = snap.Conf[row.Symbol]
			}
			entry, ok := inPlayBySymbol[row.Symbol]
			state := "idle"
			inPlayScore := 0.0
			if ok {
				state = string(entry.State)
				inPlayScore = entry.Rank
			}
			payload.Rows = append(payload.Rows, dashboardRow{
				Symbol:        market.DisplaySymbol(row.Symbol),
				RawSymbol:     row.Symbol,
				Price:         row.LastPrice,
				Score:         row.Score,
				Grade:         grade,
				State:         state,
				Confidence:    row.Confidence,
				Change24h:     row.Change24h,
				InPlayScore:   inPlayScore,
				LongScore:     sideScore(side, "long", row),
				ShortScore:    sideScore(side, "short", row),
				Bias:          biasForRow(side, row),
				Volume24h:     market.HumanUSD(row.VolumeUSD),
				OIUSD:         humanPtr(row.OIUSD),
				Funding8h:     fundingStr(row.FundingRate),
				Reason:        humanReason(row.Reason),
				Open24h:       priceStr(row.OpenPrice),
				MarkPrice:     priceStr(row.LastPrice),
				Completeness:  row.Completeness,
				Regime:        humanRegime(row.RegimeTag),
				DataFlags:     normalizeFlags(row.DataFlags),
				InPlayState:   formatInPlayState(entry, ok),
				ScannerRead:   scannerRead(side, row),
				PrimaryReason: humanReason(row.Reason),
				OpenInterest:  humanPtr(row.OIUSD),
			})
		}
		data, _ := json.Marshal(payload)
		page := statusTemplate
		tpl := template.Must(template.New("status").Parse(page))
		_ = tpl.Execute(w, map[string]any{"payload": template.JS(string(data))})
	})
}

func (s *Store) APISnapshotHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(s.Snapshot())
	})
}

func (s *Store) InPlayHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		snap := s.Snapshot()
		_ = json.NewEncoder(w).Encode(map[string]any{
			"inplay": snap.InPlay,
			"count":  len(snap.InPlay),
		})
	})
}

type dashboardPayload struct {
	Generated string            `json:"generated"`
	Exchange  string            `json:"exchange"`
	Active    []string          `json:"active"`
	Rows      []dashboardRow    `json:"rows"`
	InPlay    []dashboardInPlay `json:"inplay"`
	Engine    *EngineSnapshot   `json:"engine,omitempty"`
}

type dashboardRow struct {
	Symbol        string   `json:"symbol"`
	RawSymbol     string   `json:"rawSymbol"`
	Price         float64  `json:"price"`
	Score         float64  `json:"score"`
	Grade         string   `json:"grade"`
	State         string   `json:"state"`
	Confidence    float64  `json:"confidence"`
	Change24h     float64  `json:"change24h"`
	InPlayScore   float64  `json:"inPlayScore"`
	LongScore     float64  `json:"longScore"`
	ShortScore    float64  `json:"shortScore"`
	Bias          string   `json:"bias"`
	Volume24h     string   `json:"volume24h"`
	OIUSD         string   `json:"oiUsd"`
	Funding8h     string   `json:"funding8h"`
	Reason        string   `json:"reason"`
	Open24h       string   `json:"open24h"`
	MarkPrice     string   `json:"markPrice"`
	Completeness  float64  `json:"completeness"`
	Regime        string   `json:"regime"`
	DataFlags     []string `json:"dataFlags"`
	InPlayState   string   `json:"inPlayState"`
	ScannerRead   string   `json:"scannerRead"`
	PrimaryReason string   `json:"primaryReason"`
	OpenInterest  string   `json:"openInterest"`
}

type dashboardInPlay struct {
	Symbol   string  `json:"symbol"`
	Grade    string  `json:"grade"`
	Score    float64 `json:"score"`
	Slope    float64 `json:"slope"`
	State    string  `json:"state"`
	Momentum bool    `json:"momentum"`
	AgeMin   float64 `json:"ageMin"`
	SideBias string  `json:"sideBias"`
}

func inferDirection(exchange string) string {
	if strings.Contains(strings.ToUpper(exchange), "SHORT") {
		return "short"
	}
	return "long"
}

func sideScore(side, target string, row market.Scored) float64 {
	if side == target {
		return row.Score
	}
	if target == "long" {
		return row.Score * 0.22
	}
	return row.Score * 0.22
}

func biasForRow(side string, row market.Scored) string {
	if side == "long" {
		if row.Score >= 70 {
			return "LONG"
		}
		return "WATCH"
	}
	if row.Score >= 70 {
		return "SHORT"
	}
	return "WATCH"
}

func humanPtr(v *float64) string {
	if v == nil {
		return "-"
	}
	return "$" + market.HumanUSD(*v)
}

func fundingStr(v *float64) string {
	if v == nil {
		return "-"
	}
	return fmt.Sprintf("%.6f", *v)
}

func priceStr(v float64) string {
	if v <= 0 {
		return "-"
	}
	return fmt.Sprintf("%.4f", v)
}

func humanReason(reason string) string {
	if strings.TrimSpace(reason) == "" {
		return "No primary reason."
	}
	return strings.ReplaceAll(reason, "_", " ")
}

func humanRegime(regime string) string {
	regime = strings.ReplaceAll(strings.TrimSpace(regime), "_", " ")
	if regime == "" {
		return "mixed"
	}
	return regime
}

func normalizeFlags(flags []string) []string {
	if len(flags) == 0 {
		return nil
	}
	seen := map[string]struct{}{}
	out := make([]string, 0, len(flags))
	for _, f := range flags {
		f = strings.TrimSpace(strings.ReplaceAll(f, "_", " "))
		f = strings.ToUpper(f)
		if f == "" {
			continue
		}
		if _, ok := seen[f]; ok {
			continue
		}
		seen[f] = struct{}{}
		out = append(out, f)
	}
	return out
}

func formatInPlayState(entry inplay.Entry, ok bool) string {
	if !ok {
		return "idle"
	}
	return fmt.Sprintf("%s: grade=%s score=%.2f slope=%.3f state=%s momentum=%v",
		strings.ToUpper(entry.SideBias), entry.CurrentGrade, entry.CurrentScore, entry.ScoreSlope, entry.State, entry.Momentum)
}

func scannerRead(side string, row market.Scored) string {
	if side == "short" {
		return "Short setups currently have the stronger scanner edge."
	}
	return "Long setups currently have the stronger scanner edge."
}

const statusTemplate = `<!doctype html>
<html>
<head>
  <meta charset="utf-8">
  <meta name="viewport" content="width=device-width, initial-scale=1">
  <title>Hyperliquid Scanner</title>
  <style>
    :root{
      --bg:#09000f; --bg2:#150021; --panel:#110018; --line:#4f1482; --line-soft:#2a0a49;
      --text:#f1ecff; --muted:#b9a6d4; --cyan:#10d7d0; --green:#00d78f; --red:#ff4b78; --amber:#ffb84d;
      --chip:#2a0938;
    }
    *{box-sizing:border-box}
    body{margin:0;font-family:ui-sans-serif,system-ui,-apple-system,Segoe UI,sans-serif;background:
      radial-gradient(circle at top, rgba(98,0,175,.22), transparent 32%),
      linear-gradient(180deg,var(--bg2),var(--bg));color:var(--text)}
    .wrap{max-width:1600px;margin:0 auto;padding:28px}
    .hero{display:grid;grid-template-columns:1fr;gap:16px}
    .panel{background:rgba(8,0,18,.72);border:1px solid var(--line-soft);border-radius:24px;padding:20px;box-shadow:0 0 0 1px rgba(166,47,255,.06) inset}
    .meta{display:flex;gap:12px;align-items:center;justify-content:space-between;flex-wrap:wrap;margin-bottom:18px}
    .eyebrow{letter-spacing:.24em;text-transform:uppercase;color:var(--muted);font-size:13px}
    .title{font-size:40px;font-weight:800}
    .sub{color:var(--muted);font-size:14px}
    .grid{display:grid;grid-template-columns:1.25fr 1fr;gap:22px;margin-top:20px}
    .cards{display:grid;grid-template-columns:repeat(3,minmax(0,1fr));gap:14px;margin-top:16px}
    .card{background:rgba(8,0,18,.86);border:1px solid var(--line-soft);border-radius:18px;padding:16px}
    .card h4{margin:0 0 10px;color:var(--muted);font-weight:500;font-size:12px;letter-spacing:.08em;text-transform:uppercase}
    .value{font-size:22px;font-weight:700}
    .pill{display:inline-flex;align-items:center;padding:6px 12px;border-radius:999px;border:1px solid var(--line);background:rgba(255,255,255,.03);font-weight:700}
    .bias-long{color:var(--green);border-color:rgba(0,215,143,.45);background:rgba(0,215,143,.08)}
    .bias-short{color:#ff879d;border-color:rgba(255,75,120,.45);background:rgba(255,75,120,.10)}
    .bias-watch{color:var(--amber);border-color:rgba(255,184,77,.35);background:rgba(255,184,77,.08)}
    .kv{display:grid;grid-template-columns:repeat(2,minmax(0,1fr));gap:14px}
    .flags{display:flex;gap:10px;flex-wrap:wrap}
    .flag{padding:6px 10px;border-radius:999px;background:rgba(255,184,77,.08);border:1px solid rgba(255,184,77,.35);color:#ffcf7f;font-size:13px}
    .toolbar{display:flex;gap:12px;align-items:center;justify-content:space-between;flex-wrap:wrap;margin:18px 0}
    .engine{display:grid;grid-template-columns:1.3fr 1fr;gap:18px;margin-bottom:20px}
    .btnrow{display:flex;gap:10px;flex-wrap:wrap}
    .btn{background:#12021d;color:var(--text);border:1px solid var(--line);border-radius:10px;padding:10px 14px;cursor:pointer}
    .btn:hover{background:#1b072c}
    .poslist{display:grid;gap:10px}
    .positem{padding:12px 14px;border:1px solid var(--line-soft);border-radius:14px;background:rgba(255,255,255,.02)}
    .filters{display:flex;gap:10px;align-items:center}
    .search{background:#0d0316;border:1px solid var(--line);color:var(--text);border-radius:10px;padding:10px 12px;min-width:240px}
    .seg{display:flex;border:1px solid var(--line);border-radius:10px;overflow:hidden}
    .seg button{background:#12021d;color:var(--text);border:0;padding:10px 16px;cursor:pointer}
    .seg button.active{background:#8f22ff}
    table{width:100%;border-collapse:collapse;border-spacing:0;overflow:hidden;border-radius:18px}
    thead th{padding:16px 12px;text-align:left;font-size:13px;color:#d9c4ff;background:rgba(110,14,170,.45);border-bottom:1px solid var(--line)}
    tbody td{padding:18px 12px;border-bottom:1px solid rgba(129,59,190,.24);vertical-align:middle}
    tbody tr:hover{background:rgba(255,255,255,.03);cursor:pointer}
    tbody tr.active{background:rgba(173,59,255,.10)}
    .mono{font-family:ui-monospace,SFMono-Regular,Menlo,monospace}
    .num{text-align:right}
    .score-chip,.grade-chip,.state-chip,.inplay-chip{display:inline-flex;padding:5px 10px;border-radius:999px;border:1px solid var(--line)}
    .score-chip{background:rgba(255,255,255,.03)}
    .state-chip{background:rgba(255,255,255,.02)}
    .positive{color:var(--green)} .negative{color:#ff6d92}
    @media (max-width: 1100px){.grid{grid-template-columns:1fr}.cards{grid-template-columns:repeat(2,minmax(0,1fr))}}
    @media (max-width: 760px){.cards,.kv{grid-template-columns:1fr}.title{font-size:28px}.search{min-width:0;width:100%}}
  </style>
</head>
<body>
  <div class="wrap">
    <div class="meta">
      <div>
        <div class="eyebrow">Hyperliquid Scanner</div>
        <div class="title" id="exchange">Loading</div>
        <div class="sub" id="meta"></div>
      </div>
      <div class="pill" id="session"></div>
    </div>

    <section class="panel engine" id="engine-panel" style="display:none">
      <div>
        <div class="eyebrow">Live Engine</div>
        <div style="display:flex;align-items:center;gap:14px;margin:10px 0 18px">
          <div id="engine-mode" style="font-size:28px;font-weight:800">-</div>
          <div id="engine-state" class="pill bias-watch">IDLE</div>
        </div>
        <div class="cards" style="margin-top:0">
          <div class="card"><h4>Equity</h4><div class="value mono" id="engine-eq">-</div></div>
          <div class="card"><h4>Available</h4><div class="value mono" id="engine-avail">-</div></div>
          <div class="card"><h4>Open Positions</h4><div class="value mono" id="engine-open">-</div></div>
          <div class="card"><h4>Session</h4><div class="value mono" id="engine-session">-</div></div>
          <div class="card"><h4>Last Signal</h4><div class="value mono" id="engine-signal">-</div></div>
          <div class="card"><h4>Updated</h4><div class="value mono" id="engine-updated">-</div></div>
        </div>
        <div class="btnrow" style="margin-top:16px">
          <button class="btn" id="ctl-pause">Pause</button>
          <button class="btn" id="ctl-resume">Resume</button>
          <button class="btn" id="ctl-closeall">Close All</button>
          <button class="btn" id="ctl-close-selected">Close Selected</button>
        </div>
      </div>
      <div>
        <div class="eyebrow">Live Positions</div>
        <div class="poslist" id="engine-positions" style="margin-top:14px"></div>
      </div>
    </section>

    <div class="grid">
      <section class="panel">
        <div class="meta">
          <div>
            <div class="eyebrow">Selected Market</div>
            <div style="display:flex;align-items:center;gap:14px;margin-top:8px">
              <div id="sel-symbol" style="font-size:48px;font-weight:800">-</div>
              <div id="sel-bias" class="pill bias-watch">WATCH</div>
            </div>
            <div class="sub" id="sel-summary" style="margin-top:8px"></div>
          </div>
        </div>
        <div class="cards">
          <div class="card"><h4>Index Price</h4><div class="value mono" id="sel-index">-</div></div>
          <div class="card"><h4>Mark Price</h4><div class="value mono" id="sel-mark">-</div></div>
          <div class="card"><h4>Open 24H</h4><div class="value mono" id="sel-open">-</div></div>
          <div class="card"><h4>Grade</h4><div class="value" id="sel-grade">-</div></div>
          <div class="card"><h4>Score</h4><div class="value" id="sel-score">-</div></div>
          <div class="card"><h4>In-Play Score</h4><div class="value" id="sel-inplay">-</div></div>
          <div class="card"><h4>Long Score</h4><div class="value" id="sel-long">-</div></div>
          <div class="card"><h4>Short Score</h4><div class="value" id="sel-short">-</div></div>
          <div class="card"><h4>Confidence</h4><div class="value" id="sel-conf">-</div></div>
          <div class="card"><h4>Completeness</h4><div class="value" id="sel-complete">-</div></div>
          <div class="card"><h4>Regime</h4><div class="value mono" id="sel-regime">-</div></div>
          <div class="card"><h4>24H Change</h4><div class="value" id="sel-change">-</div></div>
          <div class="card"><h4>24H Volume</h4><div class="value" id="sel-volume">-</div></div>
          <div class="card"><h4>Open Interest</h4><div class="value" id="sel-oi">-</div></div>
          <div class="card"><h4>Funding 8H</h4><div class="value mono" id="sel-funding">-</div></div>
        </div>
      </section>

      <section class="panel">
        <div class="eyebrow">Setup Detail</div>
        <div style="font-size:24px;font-weight:800;margin:10px 0 20px" id="detail-title">Why this market is in play</div>
        <div class="card" style="margin-bottom:14px">
          <h4>Scanner Read</h4>
          <div class="value" style="font-size:16px;font-weight:500" id="detail-read">-</div>
          <div class="mono" style="margin-top:10px;color:var(--muted)" id="detail-state">-</div>
        </div>
        <div class="card" style="margin-bottom:14px">
          <h4>Primary Reason</h4>
          <div class="value" style="font-size:16px;font-weight:500" id="detail-reason">-</div>
        </div>
        <div class="card" style="margin-bottom:14px">
          <h4>Status</h4>
          <div class="kv">
            <div><div class="mono" id="detail-bias">-</div></div>
            <div><div class="mono" id="detail-status">ACTIVE</div></div>
          </div>
        </div>
        <div class="card">
          <h4>Data Flags</h4>
          <div class="flags" id="detail-flags"></div>
        </div>
      </section>
    </div>

    <div class="toolbar">
      <div class="filters">
        <input id="search" class="search" placeholder="Search markets...">
        <div class="seg">
          <button data-filter="all" class="active">All</button>
          <button data-filter="gainers">Gainers</button>
          <button data-filter="losers">Losers</button>
        </div>
      </div>
      <div class="sub" id="updated"></div>
    </div>

    <section class="panel" style="padding:0;overflow:hidden">
      <table>
        <thead>
          <tr>
            <th>SYMBOL</th><th class="num">PRICE</th><th class="num">SCORE</th><th>GRADE</th><th>STATE</th><th class="num">CONF</th><th class="num">24H %</th><th class="num">IN PLAY</th><th class="num">LONG</th><th class="num">SHORT</th><th>BIAS</th><th class="num">VOLUME 24H</th><th class="num">OI USD</th><th class="num">FUNDING 8H</th><th>REASON</th>
          </tr>
        </thead>
        <tbody id="rows"></tbody>
      </table>
    </section>
  </div>
  <script>
    const payload = {{.payload}};
    let rows = payload.rows || [];
    let engine = payload.engine || null;
    let selected = rows[0] || null;
    let filter = 'all';
    let query = '';
    const fmtPct = v => (v >= 0 ? '' : '-') + Math.abs(v).toFixed(2) + '%';
    const fmtConf = v => Math.round((v || 0) * 100) + '%';
    const normReason = v => (v || '').replaceAll('_', ' ');
    const byId = id => document.getElementById(id);

    function biasClass(v){
      if(v === 'LONG') return 'bias-long';
      if(v === 'SHORT') return 'bias-short';
      return 'bias-watch';
    }
    function fmtUsd(v){
      const num = Number(v || 0);
      return num.toFixed(2);
    }
    async function engineControl(action, symbol){
      if(!engine || !engine.controlUrl) return;
      const body = {action};
      if(symbol) body.symbol = symbol;
      try{
        await fetch(engine.controlUrl, {
          method:'POST',
          headers:{'Content-Type':'application/json'},
          body: JSON.stringify(body)
        });
      } catch (e) {
        console.error(e);
      }
    }
    function renderEngine(){
      const panel = byId('engine-panel');
      if(!engine){
        panel.style.display = 'none';
        return;
      }
      panel.style.display = 'grid';
      byId('engine-mode').textContent = (engine.mode || 'engine').toUpperCase();
      const state = byId('engine-state');
      state.textContent = engine.paused ? 'PAUSED' : 'ACTIVE';
      state.className = 'pill ' + (engine.paused ? 'bias-short' : 'bias-long');
      byId('engine-eq').textContent = fmtUsd(engine.account?.equity);
      byId('engine-avail').textContent = fmtUsd(engine.account?.availableUsd);
      byId('engine-open').textContent = String(engine.account?.openCount || 0);
      byId('engine-session').textContent = engine.session || '-';
      byId('engine-signal').textContent = engine.lastSignal || 'none';
      byId('engine-updated').textContent = engine.updatedAt ? new Date(engine.updatedAt).toLocaleTimeString() : '-';
      const posWrap = byId('engine-positions');
      posWrap.innerHTML = '';
      (engine.positions || []).forEach(pos => {
        const el = document.createElement('div');
        el.className = 'positem mono';
        el.textContent = pos.symbol + ' ' + pos.side +
          ' qty=' + Number(pos.size||0).toFixed(4) +
          ' entry=' + Number(pos.entry||0).toFixed(4) +
          ' mark=' + Number(pos.mark||0).toFixed(4) +
          ' stop=' + Number(pos.stop||0).toFixed(4) +
          ' target=' + Number(pos.target||0).toFixed(4) +
          ' pnl=' + Number(pos.pnl||0).toFixed(2);
        posWrap.appendChild(el);
      });
      byId('ctl-pause').onclick = () => engineControl('pause');
      byId('ctl-resume').onclick = () => engineControl('resume');
      byId('ctl-closeall').onclick = () => engineControl('closeall');
      byId('ctl-close-selected').onclick = () => selected && engineControl('close', selected.rawSymbol);
    }
    function applyFilter(list){
      return list.filter(r => {
        if(query && !r.symbol.toLowerCase().includes(query) && !r.reason.toLowerCase().includes(query)) return false;
        if(filter === 'gainers' && !(r.change24h > 0)) return false;
        if(filter === 'losers' && !(r.change24h < 0)) return false;
        return true;
      });
    }
    function renderSelected(row){
      if(!row) return;
      byId('sel-symbol').textContent = row.symbol;
      const bias = byId('sel-bias');
      bias.textContent = row.bias;
      bias.className = 'pill ' + biasClass(row.bias);
      byId('sel-summary').textContent = row.bias === 'SHORT' ? 'Downside pressure' : row.bias === 'LONG' ? 'Upside pressure' : 'Watchlist candidate';
      byId('sel-index').textContent = row.markPrice;
      byId('sel-mark').textContent = row.markPrice;
      byId('sel-open').textContent = row.open24h;
      byId('sel-grade').textContent = row.grade;
      byId('sel-score').textContent = row.score.toFixed(2);
      byId('sel-inplay').textContent = Math.round(row.inPlayScore).toString();
      byId('sel-long').textContent = Math.round(row.longScore).toString();
      byId('sel-short').textContent = Math.round(row.shortScore).toString();
      byId('sel-conf').textContent = fmtConf(row.confidence);
      byId('sel-complete').textContent = fmtConf(row.completeness);
      byId('sel-regime').textContent = row.regime;
      byId('sel-change').textContent = fmtPct(row.change24h);
      byId('sel-volume').textContent = '$' + row.volume24h;
      byId('sel-oi').textContent = row.openInterest;
      byId('sel-funding').textContent = row.funding8h;
      byId('detail-title').textContent = 'Why ' + row.rawSymbol + ' is in play';
      byId('detail-read').textContent = row.scannerRead;
      byId('detail-state').textContent = row.inPlayState;
      byId('detail-reason').textContent = row.primaryReason;
      byId('detail-bias').textContent = row.bias + ': ' + row.grade + ' @ ' + row.score.toFixed(2);
      byId('detail-status').textContent = row.state.toUpperCase();
      const flags = byId('detail-flags');
      flags.innerHTML = '';
      (row.dataFlags || []).forEach(f => {
        const el = document.createElement('span');
        el.className = 'flag';
        el.textContent = f;
        flags.appendChild(el);
      });
    }
    function renderTable(){
      const tbody = byId('rows');
      tbody.innerHTML = '';
      const filtered = applyFilter(rows);
      if(selected && !filtered.find(r => r.rawSymbol === selected.rawSymbol)) {
        selected = filtered[0] || rows[0] || null;
      }
      filtered.forEach(row => {
        const tr = document.createElement('tr');
        if(selected && row.rawSymbol === selected.rawSymbol) tr.className = 'active';
        tr.onclick = () => { selected = row; renderSelected(row); renderTable(); };
        tr.innerHTML =
          '<td>' + row.symbol + '</td>' +
          '<td class="num mono">' + row.markPrice + '</td>' +
          '<td class="num">' + row.score.toFixed(2) + '</td>' +
          '<td><span class="grade-chip">' + row.grade + '</span></td>' +
          '<td><span class="state-chip">' + row.state + '</span></td>' +
          '<td class="num">' + fmtConf(row.confidence) + '</td>' +
          '<td class="num ' + (row.change24h >= 0 ? 'positive' : 'negative') + '">' + fmtPct(row.change24h) + '</td>' +
          '<td class="num"><span class="score-chip">' + Math.round(row.inPlayScore) + '</span></td>' +
          '<td class="num"><span class="score-chip">' + Math.round(row.longScore) + '</span></td>' +
          '<td class="num"><span class="score-chip">' + Math.round(row.shortScore) + '</span></td>' +
          '<td><span class="pill ' + biasClass(row.bias) + '">' + row.bias + '</span></td>' +
          '<td class="num">$' + row.volume24h + '</td>' +
          '<td class="num">' + row.oiUsd + '</td>' +
          '<td class="num mono">' + row.funding8h + '</td>' +
          '<td>' + row.reason + '</td>';
        tbody.appendChild(tr);
      });
      renderSelected(selected || filtered[0] || rows[0]);
      renderEngine();
      byId('updated').textContent = 'Updated ' + new Date(payload.generated).toLocaleTimeString() + ' • ' + filtered.length + ' qualified assets';
    }
    byId('exchange').textContent = payload.exchange || 'Scanner';
    byId('meta').textContent = 'Generated ' + new Date(payload.generated).toLocaleString();
    byId('session').textContent = (payload.active || []).join(', ') || 'ACTIVE';
    document.querySelectorAll('[data-filter]').forEach(btn => btn.onclick = () => {
      document.querySelectorAll('[data-filter]').forEach(x => x.classList.remove('active'));
      btn.classList.add('active');
      filter = btn.dataset.filter;
      renderTable();
    });
    byId('search').addEventListener('input', e => { query = e.target.value.trim().toLowerCase(); renderTable(); });
    renderTable();
  </script>
</body>
</html>`
