import hype_funcs as hype
import time

def compute_structure(info, coin):

    now = int(time.time()*1000)
    past = now - 6*60*60*1000

    candles = hype.candles(info, coin, "15m", past, now)

    profile, poc, _ = hype.session_volume_profile(candles, 100)

    price = float(candles[-1]["c"])

    structure = hype.build_market_structure(profile, poc, price)

    scores = hype.directional_scanner_scores(structure, price)

    return {
        "regime": structure["regime"],
        "long_score": scores["long_score"],
        "short_score": scores["short_score"],
        "long_reason": scores["long_reason"],
        "short_reason": scores["short_reason"],
        "VAL": structure["VAL"],
        "VAH": structure["VAH"],
        "price": price,
    }
