# hype_funcs.py
import os
import eth_account
from hyperliquid.info import Info
from hyperliquid.exchange import Exchange
from hyperliquid.utils import constants



def clear_proxy_env():
    # Fixes many SSL: WRONG_VERSION_NUMBER cases caused by proxy env vars
    for k in ["HTTPS_PROXY", "https_proxy", "HTTP_PROXY", "http_proxy", "ALL_PROXY", "all_proxy"]:
        os.environ.pop(k, None)
    os.environ["NO_PROXY"] = "api.hyperliquid-testnet.xyz,api.hyperliquid.xyz"


def get_base_url(use_testnet: bool) -> str:
    return constants.TESTNET_API_URL if use_testnet else constants.MAINNET_API_URL


def create_clients(private_key: str, use_testnet: bool):
    """
    Creates wallet + Info + Exchange. Done lazily (not at import time).
    """
    clear_proxy_env()

    base_url = get_base_url(use_testnet)
    wallet = eth_account.Account.from_key(private_key)
    account_address = wallet.address

    info = Info(base_url, skip_ws=True)
    exchange = Exchange(wallet, base_url, account_address=account_address)
    return base_url, account_address, info, exchange


def get_best_bid_ask(info: Info, coin: str) -> tuple[float, float]:
    l2 = info.l2_snapshot(coin)
    bids, asks = l2["levels"]
    return float(bids[0]["px"]), float(asks[0]["px"])


def get_sz_decimals(info: Info, coin: str) -> int:
    meta = info.meta()
    asset = next((a for a in meta["universe"] if a["name"] == coin), None)
    if not asset:
        raise ValueError(f"Coin not found in meta(): {coin}")
    return int(asset["szDecimals"])


def round_size(sz: float, sz_decimals: int) -> float:
    return round(float(sz), sz_decimals)


def place_limit(exchange: Exchange, coin: str, is_buy: bool, sz: float, px: float, reduce_only: bool = False):
    order_type = {"limit": {"tif": "Gtc"}}  # correct dict shape

    try:
        return exchange.order(coin, is_buy, sz, px, order_type, reduce_only)
    except TypeError:
        # fallback for SDK versions without reduce_only arg
        if reduce_only:
            print("[WARN] SDK version doesn't support reduce_only param; placing without it.")
        return exchange.order(coin, is_buy, sz, px, order_type)
    

def l2(info: Info, coin: str):
    return info.l2_snapshot(coin)


def candles(info: Info, coin: str, interval, start, end):
    return info.candles_snapshot(coin, interval, start, end)

def funding_history(info: Info, coin: str, start, end):
    return info.funding_history(coin, start, end)

def all_mids(info: Info):
    return info.all_mids()
    
def meta_and_asset_ctxs(info: Info, coin: str):
    meta, asset_ctxs = info.meta_and_asset_ctxs()

    universe = meta["universe"]
    markets = {}

    for coin_meta, ctx in zip(universe, asset_ctxs):
        if coin_meta.get("isDelisted"):
            continue

        symbol = coin_meta["name"]

        markets[symbol] = {
            "max_leverage": coin_meta["maxLeverage"],
            "funding": float(ctx["funding"]) * 100,
            "oi": float(ctx["openInterest"]),
            "mark": float(ctx["markPx"]),
            "oracle": float(ctx["oraclePx"]),
            "volume": float(ctx["dayNtlVlm"]),
        }

    return markets.get(coin)

def session_volume_profile(candles, bin_size=50):
    """
    candles: list of dicts from hype.candles()
    bin_size: price resolution (ex: $25, $50, $100)
    """

    # 1. Find session range
    lows = [float(c["l"]) for c in candles]
    highs = [float(c["h"]) for c in candles]

    session_low = min(lows)
    session_high = max(highs)

    # 2. Build price bins
    bins = {}
    price = session_low - (session_low % bin_size)

    while price <= session_high:
        bins[price] = 0.0
        price += bin_size

    # 3. Distribute volume into bins
    for c in candles:
        low = float(c["l"])
        high = float(c["h"])
        vol = float(c["v"])

        touched_bins = []

        for b in bins:
            if b >= low and b <= high:
                touched_bins.append(b)

        if not touched_bins:
            continue

        vol_per_bin = vol / len(touched_bins)

        for b in touched_bins:
            bins[b] += vol_per_bin

    # 4. Sort by price
    profile = dict(sorted(bins.items()))

    # 5. Find POC
    poc_price = max(profile, key=profile.get)
    poc_volume = profile[poc_price]

    return profile, poc_price, poc_volume

def compute_value_area(profile, poc, value_pct=0.70):
    prices = list(profile.keys())
    total_vol = sum(profile.values())
    target_vol = total_vol * value_pct

    poc_idx = prices.index(poc)

    va_low = va_high = poc
    cum_vol = profile[poc]

    left = poc_idx - 1
    right = poc_idx + 1

    while cum_vol < target_vol and (left >= 0 or right < len(prices)):
        left_vol = profile[prices[left]] if left >= 0 else 0
        right_vol = profile[prices[right]] if right < len(prices) else 0

        if right_vol >= left_vol:
            cum_vol += right_vol
            va_high = prices[right]
            right += 1
        else:
            cum_vol += left_vol
            va_low = prices[left]
            left -= 1

    return va_low, va_high

def detect_hvn_lvn(profile, high_mult=1.5, low_mult=0.5):
    vols = list(profile.values())
    avg_vol = sum(vols) / len(vols)

    hvns = []
    lvns = []

    for price, vol in profile.items():
        if vol >= avg_vol * high_mult:
            hvns.append(price)
        elif vol <= avg_vol * low_mult:
            lvns.append(price)

    return hvns, lvns

def classify_regime(price, val, vah):
    if val <= price <= vah:
        return "BALANCED"
    return "IMBALANCED"

def build_market_structure(profile, poc, current_price):

    val, vah = compute_value_area(profile, poc)
    hvns, lvns = detect_hvn_lvn(profile)
    regime = classify_regime(current_price, val, vah)

    return {
        "POC": poc,
        "VAL": val,
        "VAH": vah,
        "HVNs": hvns,
        "LVNs": lvns,
        "regime": regime
    }

def directional_scanner_scores(structure, price, levels=None):
    long_score = 0.0
    short_score = 0.0
    long_reasons = []
    short_reasons = []

    val = float(structure["VAL"])
    vah = float(structure["VAH"])
    poc = float(structure["POC"])

    if price > vah:
        long_score += 65
        long_reasons.append("above_VAH")
    elif price < val:
        short_score += 65
        short_reasons.append("below_VAL")
    else:
        if price >= poc:
            long_score += 20
            long_reasons.append("inside_range_upper")
        if price <= poc:
            short_score += 20
            short_reasons.append("inside_range_lower")

    if structure["regime"] == "IMBALANCED":
        if price >= poc:
            long_score += 20
            long_reasons.append("imbalanced")
        if price <= poc:
            short_score += 20
            short_reasons.append("imbalanced")

    if levels and len(levels) == 2 and levels[0] and levels[1]:
        bids, asks = levels
        bid_liq = sum(float(b["sz"]) for b in bids[:3])
        ask_liq = sum(float(a["sz"]) for a in asks[:3])
        if bid_liq > ask_liq * 1.15:
            long_score += 15
            long_reasons.append("bid_pressure")
        elif ask_liq > bid_liq * 1.15:
            short_score += 15
            short_reasons.append("ask_pressure")

    return {
        "long_score": min(round(long_score, 2), 100.0),
        "short_score": min(round(short_score, 2), 100.0),
        "long_reason": " + ".join(long_reasons) if long_reasons else "none",
        "short_reason": " + ".join(short_reasons) if short_reasons else "none",
    }

def compute_delta(trades):
    buy_vol = 0.0
    sell_vol = 0.0

    for t in trades:
        sz = float(t["sz"])
        if t["side"] == "B":
            buy_vol += sz
        else:
            sell_vol += sz

    delta = buy_vol - sell_vol
    return delta, buy_vol, sell_vol

class CVDTracker:
    def __init__(self):
        self.cvd = 0.0
        self.history = []

    def update(self, delta, ts):
        self.cvd += delta
        self.history.append((ts, self.cvd))

        if len(self.history) > 500:
            self.history.pop(0)

        return self.cvd
    
def cvd_slope(cvd_history, lookback=20):
    if len(cvd_history) < lookback:
        return 0.0

    old = cvd_history[-lookback][1]
    now = cvd_history[-1][1]
    return now - old

def classify_cvd_slope(slope, threshold=1.5):

    if slope > threshold:
        return "STRONG_BUY"
    if slope < -threshold:
        return "STRONG_SELL"
    return "FLAT"

def detect_buy_absorption(trades, l2_bids, price_floor):
    sell_vol = sum(float(t["sz"]) for t in trades if t["side"] == "A")
    best_bid = float(l2_bids[0]["px"])
    bid_liq = sum(float(b["sz"]) for b in l2_bids[:3])

    if sell_vol > 2.0 and best_bid >= price_floor and bid_liq > 5.0:
        return True
    return False

def detect_sell_absorption(trades, l2_asks, price_ceiling):
    buy_vol = sum(float(t["sz"]) for t in trades if t["side"] == "B")
    best_ask = float(l2_asks[0]["px"])
    ask_liq = sum(float(a["sz"]) for a in l2_asks[:3])

    if buy_vol > 2.0 and best_ask <= price_ceiling and ask_liq > 5.0:
        return True
    return False

def detect_buy_initiation(delta, cvd_slope, price, level, l2_asks):
    ask_liq = sum(float(a["sz"]) for a in l2_asks[:3])

    if (
        delta > 1.5 and
        cvd_slope > 2.0 and
        price > level and
        ask_liq < 1.5
    ):
        return True
    return False

def detect_sell_initiation(delta, cvd_slope, price, level, l2_bids):
    bid_liq = sum(float(b["sz"]) for b in l2_bids[:3])

    if (
        delta < -1.5 and
        cvd_slope < -2.0 and
        price < level and
        bid_liq < 1.5
    ):
        return True
    return False

def order_flow_state(
    trades,
    l2,
    cvd_tracker,
    current_price,
    structure   # from Phase 2.1
):
    delta, buy_vol, sell_vol = compute_delta(trades)
    cvd = cvd_tracker.update(delta, trades[-1]["time"])
    slope = cvd_slope(cvd_tracker.history)
    slope_state = classify_cvd_slope(slope)

    bids, asks = l2["levels"]

    buy_abs = detect_buy_absorption(trades, bids, structure["VAL"])
    sell_abs = detect_sell_absorption(trades, asks, structure["VAH"])

    buy_init = detect_buy_initiation(
        delta, slope, current_price, structure["VAH"], asks
    )
    sell_init = detect_sell_initiation(
        delta, slope, current_price, structure["VAL"], bids
    )

    return {
        "delta": delta,
        "cvd": cvd,
        "cvd_slope": slope,
        "cvd_state": slope_state,
        "buy_absorption": buy_abs,
        "sell_absorption": sell_abs,
        "buy_initiation": buy_init,
        "sell_initiation": sell_init
    }
