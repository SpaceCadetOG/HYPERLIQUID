import time
import hype_funcs as hype

COINS = ["BTC","ETH","SOL","XRP"]

def fetch_market(info):
    mids = hype.all_mids(info)

    rows = []

    for c in COINS:
        price = float(mids[c])

        market = hype.meta_and_asset_ctxs(info, c)

        rows.append({
            "Symbol": c,
            "Price": round(price,2),
            "Vol24h": round(market["volume"]/1e6,2),
            "OI": round(market["oi"]/1e6,2),
            "Funding": round(market["funding"],4)
        })

    return rows