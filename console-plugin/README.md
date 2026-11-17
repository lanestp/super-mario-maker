# Console plugin

An [Aroma](https://aroma.foryour.cafe) plugin for a real Wii U that sends Super Mario Maker, and nothing else, to this server. The console stays signed in to the network it uses, such as Pretendo, for its account, friends and every other game.

When a game wants its game server, it asks the console's account system for a NEX token for the game server's ID. The token says where the server is and the password to log in with. The plugin lets the console's network answer as usual, then changes the answer for Super Mario Maker's three game server IDs only, and only for the one player it was built for.

The game logs in with the console's player ID on its own network, so the player needs an account here with that same ID. `smm-account console <username> <PID>` makes one.

**Not yet tried on a console.** It builds, and an account made this way works with a client that logs in the way the plugin makes the game log in. Whether the console's account system and the game behave as expected has not been seen.

## Building

`scripts/make-console-bundle.py <username> <PID>` makes the account, builds the plugin and writes a zip for the player's SD card. To build by hand:

```bash
docker build --platform linux/amd64 -t smm-wiiu-build console-plugin
docker run --rm --platform linux/amd64 -v "$PWD/console-plugin":/app smm-wiiu-build \
	make SMM_HOST=203.0.113.4 SMM_PORT=6004 SMM_PID=1712345678 SMM_NEX_PASSWORD=...
```

`SMM_HOST` must be an IPv4 address, as the token has room for 15 characters.
