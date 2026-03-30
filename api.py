# api.py

import asyncio
import hype_funcs as hype
import secret as s

from fastapi import FastAPI
from fastapi.middleware.cors import CORSMiddleware

from scanner import build_scanner
from live_cache import live_data

app = FastAPI()

app.add_middleware(
    CORSMiddleware,
    allow_origins=["*"],
    allow_methods=["*"],
    allow_headers=["*"],
)

COINS = ["BTC", "ETH", "SOL", "XRP"]

USE_TESTNET = False

base_url, account, info, exchange = hype.create_clients(
    s.private_key,
    USE_TESTNET
)

# 🔄 BACKGROUND LOOP (1 second refresh)
async def refresh_loop():

    while True:

        try:
            # markets
            mids = hype.all_mids(info)

            markets = []
            for c in COINS:
                markets.append({
                    "symbol": c,
                    "price": float(mids[c])
                })

            live_data["markets"] = markets

            # scanner
            live_data["scanner"] = build_scanner(info, COINS)

        except Exception as e:
            print("Refresh error:", e)

        await asyncio.sleep(1)


@app.on_event("startup")
async def startup_event():
    asyncio.create_task(refresh_loop())


@app.get("/markets")
def get_markets():
    return live_data["markets"]


@app.get("/scanner")
def get_scanner():
    return live_data["scanner"]


@app.get("/symbol/{symbol}")
def get_symbol(symbol: str):
    return next(
        (s for s in live_data["scanner"] if s["symbol"] == symbol),
        {}
    )