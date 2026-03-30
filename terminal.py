import time
import json
import os
import pandas as pd
from hype_funcs_ws import HyperliquidWS


REFRESH = 1
MAX_MARKETS = 40   # instant load

GREEN = "\033[92m"
CYAN = "\033[96m"
RESET = "\033[0m"
BOLD = "\033[1m"


def clear():
    os.system("cls" if os.name == "nt" else "clear")


print(f"{CYAN}⚡ Booting Hyperliquid WebSocket Scanner...{RESET}")

with open("symbols.json") as f:
    ALL = json.load(f)

SYMBOLS = ALL[:MAX_MARKETS]   # fast startup


ws = HyperliquidWS()
ws.connect()

time.sleep(0.5)

for s in SYMBOLS:
    ws.subscribe_trades(s)

print(f"{GREEN}Streaming {len(SYMBOLS)} liquid markets{RESET}")
time.sleep(0.5)


try:
    while True:
        clear()
        print(f"{BOLD}⚡ Hyperliquid Live Scanner{RESET}\n")

        rows = []

        for sym in SYMBOLS:
            if sym in ws.prices:
                rows.append({
                    "Symbol": sym,
                    "Price": round(ws.prices[sym], 6),
                })

        if rows:
            df = pd.DataFrame(rows)
            print(df.to_string(index=False))

        print(f"\n{CYAN}Live WebSocket feed — {len(SYMBOLS)} markets{RESET}")
        time.sleep(REFRESH)

except KeyboardInterrupt:
    print("\nScanner stopped.")