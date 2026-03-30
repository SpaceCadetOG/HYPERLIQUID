# dashboard.py

import time
import pandas as pd
import streamlit as st

import secret as s
import hype_funcs as hype
from hype_funcs import session_volume_profile, build_market_structure


COINS = ["BTC", "ETH", "SOL", "XRP"]
USE_TESTNET = False
BIN_SIZE = 100
WINDOW = 24   # last 24 candles (1h)


st.set_page_config(layout="wide")
st.title("⚡ Hyperliquid Live Scanner")

placeholder = st.empty()


def compute_row(info, coin):

    now = int(time.time() * 1000)
    start = now - WINDOW * 60 * 60 * 1000

    candles = hype.candles(info, coin, "1h", start, now)

    if len(candles) < 5:
        return None

    profile, poc, _ = session_volume_profile(candles, BIN_SIZE)

    price = float(hype.all_mids(info)[coin])

    structure = build_market_structure(profile, poc, price)

    score = 70 if structure["regime"] == "IMBALANCED" else 0

    return {
        "Symbol": coin,
        "Price": round(price, 2),
        "Score": score,
        "Regime": structure["regime"],
        "VAL": structure["VAL"],
        "VAH": structure["VAH"]
    }


def main():

    base_url, account, info, _ = hype.create_clients(
        s.private_key, USE_TESTNET
    )

    while True:

        rows = []

        for coin in COINS:
            row = compute_row(info, coin)
            if row:
                rows.append(row)

        df = pd.DataFrame(rows)

        with placeholder.container():
            st.dataframe(
                df,
                use_container_width=True,
                hide_index=True
            )

        time.sleep(1)


if __name__ == "__main__":
    main()