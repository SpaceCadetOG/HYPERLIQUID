import json
import threading
import websocket

WS_URL = "wss://api.hyperliquid.xyz/ws"


class HyperliquidWS:
    def __init__(self):
        self.ws = None
        self.prices = {}
        self.books = {}
        self.trades = {}

    def connect(self):
        self.ws = websocket.WebSocketApp(
            WS_URL,
            on_open=self.on_open,
            on_message=self.on_message
        )
        t = threading.Thread(target=self.ws.run_forever, daemon=True)
        t.start()

    def on_open(self, ws):
        print("✅ WebSocket connected")

    def on_message(self, ws, msg):
        data = json.loads(msg)

        if data.get("channel") == "allMids":
            for k, v in data["data"].items():
                self.prices[k] = float(v)

        if data.get("channel") == "l2Book":
            coin = data["data"]["coin"]
            self.books[coin] = data["data"]["levels"]

        if data.get("channel") == "trades":
            coin = data["data"][0]["coin"]
            self.trades[coin] = data["data"]

    def subscribe_prices(self):
        self.ws.send(json.dumps({
            "method": "subscribe",
            "subscription": {"type": "allMids"}
        }))

    def subscribe_book(self, coin):
        self.ws.send(json.dumps({
            "method": "subscribe",
            "subscription": {"type": "l2Book", "coin": coin}
        }))

    def subscribe_trades(self, coin):
        self.ws.send(json.dumps({
            "method": "subscribe",
            "subscription": {"type": "trades", "coin": coin}
        }))