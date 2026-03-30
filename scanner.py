import time
import pandas as pd
import streamlit as st

import secret as s
import hype_funcs as hype
from hype_funcs import session_volume_profile, build_market_structure
from hype_funcs_ws import HyperliquidWS


# ───────────────────────── UI ─────────────────────────

st.set_page_config(layout="wide")
st.title("⚡ Hyperliquid Real-Time Scanner")

st.markdown("""
<style>
.regime-green {
    background: rgba(0,255,150,.25);
    color:#00ff9c;
    font-weight:800;
    text-shadow:0 0 10px rgba(0,255,150,.9);
}
.regime-red {
    background: rgba(255,60,60,.25);
    color:#ff6b6b;
    font-weight:800;
    text-shadow:0 0 10px rgba(255,80,80,.9);
}
</style>
""", unsafe_allow_html=True)


# ───────────────────────── SETTINGS ─────────────────────────

INTERVAL = "1h"
BIN_SIZE = 100
FAST_REFRESH = 1          # seconds
STRUCTURE_REFRESH = 30    # seconds


# ───────────────────────── BOOT CLIENTS ─────────────────────────

@st.cache_resource
def boot():
    return hype.create_clients(s.private_key, False)

base_url, account, info, _ = boot()


# ───────────────────────── LOAD SYMBOLS ONCE ─────────────────────────

@st.cache_data(ttl=600)
def load_symbols():
    meta = info.meta()
    return [a["name"] for a in meta["universe"] if not a.get("isDelisted")]

SYMBOLS = load_symbols()


# ───────────────────────── WEBSOCKET ─────────────────────────

ws = HyperliquidWS()
ws.connect()

time.sleep(1)

ws.subscribe_prices()

# Subscribe to books (gives price + depth)
for sym in SYMBOLS:
    ws.subscribe_book(sym)


# ───────────────────────── STRUCTURE CACHE ─────────────────────────

structure_cache = {}
last_structure_update = 0


def update_structure():
    global last_structure_update

    now_time = time.time()
    now = int(now_time * 1000)
    one_day = now - 86400000

    for sym in SYMBOLS:
        try:
            candles = hype.candles(info, sym, INTERVAL, one_day, now)
            if len(candles) < 12:
                continue

            price = ws.prices.get(sym, 0)

            profile, poc, _ = session_volume_profile(candles, BIN_SIZE)
            structure = build_market_structure(profile, poc, price)

            structure_cache[sym] = structure

        except:
            continue

    last_structure_update = now_time


# ───────────────────────── STREAMLIT LOOP ─────────────────────────

header = st.empty()
left_col, right_col = st.columns(2)

while True:

    now_time = time.time()

    # ───── Update structure every 30s (slow layer) ─────
    if now_time - last_structure_update > STRUCTURE_REFRESH:
        update_structure()

    # ───── Fast 1s layer (price only) ─────
    rows = []

    for sym in SYMBOLS:

        if sym not in ws.prices:
            continue

        if sym not in structure_cache:
            continue

        try:
            price = ws.prices[sym]
            structure = structure_cache[sym]

            levels = ws.books.get(sym)
            scores = hype.directional_scanner_scores(structure, price, levels)

            rows.append({
                "Symbol": sym,
                "Price": round(price, 6),
                "LongScore": scores["long_score"],
                "ShortScore": scores["short_score"],
                "LongReason": scores["long_reason"],
                "ShortReason": scores["short_reason"],
                "Regime": structure["regime"],
                "VAL": structure["VAL"],
                "VAH": structure["VAH"]
            })

        except:
            continue

    df = pd.DataFrame(rows)

    if not df.empty:

        long_df = df.sort_values("LongScore", ascending=False).reset_index(drop=True)
        short_df = df.sort_values("ShortScore", ascending=False).reset_index(drop=True)

        with header.container():
            st.caption("Directional scanner based on price vs VAH/VAL plus top-of-book pressure.")

        with left_col:
            st.subheader("LONGS")
            styled_long = long_df[["Symbol", "Price", "LongScore", "LongReason", "Regime", "VAL", "VAH"]]
            st.dataframe(
                styled_long,
                use_container_width=True,
                height=800,
                hide_index=True,
            )

        with right_col:
            st.subheader("SHORTS")
            styled_short = short_df[["Symbol", "Price", "ShortScore", "ShortReason", "Regime", "VAL", "VAH"]]
            st.dataframe(
                styled_short,
                use_container_width=True,
                height=800,
                hide_index=True,
            )

    time.sleep(FAST_REFRESH)
