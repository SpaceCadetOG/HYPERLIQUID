import time
import secret as s
import hype_funcs as hype
from hype_funcs_ws import HyperliquidWS
from collections import deque
from datetime import datetime, timedelta, timezone

COIN = "BTC"
USE_TESTNET = False

# =============================
# SESSION WINDOW (UTC MIDNIGHT)
# =============================

def session_window():
    now_dt = datetime.now(timezone.utc)
    today_00 = datetime.combine(now_dt.date(), datetime.min.time(), tzinfo=timezone.utc)
    yesterday_00 = today_00 - timedelta(days=1)
    return (
        int(yesterday_00.timestamp() * 1000),
        int(today_00.timestamp() * 1000)
    )

one_day_ago, now = session_window()

# =============================
# CLIENTS
# =============================

base_url, addr, info, exchange = hype.create_clients(
    s.private_key,
    USE_TESTNET
)

# =============================
# BUILD MARKET STRUCTURE
# =============================

def build_structure():
    candles = hype.candles(info, COIN, "1h", one_day_ago, now)
    profile, poc, _ = hype.session_volume_profile(candles)
    mid = float(hype.all_mids(info)[COIN])
    return hype.build_market_structure(profile, poc, mid)

structure = build_structure()

print("\n📊 MARKET STRUCTURE:")
for k, v in structure.items():
    print(f"{k}: {v}")

# =============================
# ORDER FLOW TRACKING
# =============================

cvd_tracker = hype.CVDTracker()
recent_trades = deque(maxlen=300)
latest_l2 = None

# =============================
# AUTO STRUCTURE REFRESH (HOURLY)
# =============================

last_refresh = time.time()

def maybe_refresh_structure():
    global structure, one_day_ago, now, last_refresh

    if time.time() - last_refresh > 3600:
        one_day_ago, now = session_window()
        structure = build_structure()
        last_refresh = time.time()

        print("\n🔄 STRUCTURE UPDATED:")
        for k, v in structure.items():
            print(f"{k}: {v}")

# =============================
# WEBSOCKET CALLBACKS
# =============================

def on_trade(trade):
    global latest_l2

    recent_trades.append(trade)

    if latest_l2 is None or len(recent_trades) < 30:
        return

    maybe_refresh_structure()

    state = hype.order_flow_state(
        list(recent_trades),
        latest_l2,
        cvd_tracker,
        float(trade["px"]),
        structure
    )

    if state["confidence"] >= 0.7:
        print("\n🔥 HIGH CONFIDENCE SIGNAL")
        for k, v in state.items():
            print(f"{k}: {v}")

def on_l2(book):
    global latest_l2
    latest_l2 = book

# =============================
# START STREAMS
# =============================

ws = HyperliquidWS()
ws.trade_cb = on_trade
ws.book_cb = on_l2

ws.connect()
time.sleep(1)

ws.subscribe_trades(COIN)
ws.subscribe_orderbook(COIN)

print("\n🚀 Live order flow engine running...\n")

while True:
    time.sleep(1)