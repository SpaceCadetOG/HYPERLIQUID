import secret as s
import hype_funcs as hype
import json

base_url, acct, info, _ = hype.create_clients(s.private_key, False)

meta = info.meta()

symbols = [a["name"] for a in meta["universe"] if not a.get("isDelisted")]

with open("symbols.json", "w") as f:
    json.dump(symbols, f)

print("Saved", len(symbols), "symbols")
