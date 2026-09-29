# Emby

Use cliamp to stream music from an Emby server through Emby's authenticated HTTP API. Emby opens in an artists-first hierarchy by default: artist, then album, then songs. This is the same as the Jellyfin provider.

> **Quick start:** Run `cliamp setup`. Select API-key or username+password authentication. The TUI validates `/System/Info` and writes the `[emby]` block. Manual steps follow.

## Prerequisites

- A reachable Emby server
- At least one library with `CollectionType = music`
- An Emby API key or user credentials

## Configuration

Add an `[emby]` section to `~/.config/cliamp/config.toml`:

```toml
[emby]
url = "https://emby.example.com"
user = "alice"
password = "your_password_here"
# optional alternatives:
# token = "xxxxxxxxxxxxxxxxxxxx"
# user_id = "00000000000000000000000000000000"
```

| Key | Description |
|-----|-------------|
| `url` | Base URL of your Emby server |
| `user` | Emby username. Use it for password login and to select the account for an API key. |
| `password` | Emby password for password login |
| `token` | Emby API key. Use it instead of a username and password. |
| `user_id` | Optional Emby user id to skip discovery |

## Usage

After configuration, **Emby** appears in the provider list.

To start cliamp with Emby selected:

```bash
cliamp --provider emby
```

Or set the provider in configuration:

```toml
provider = "emby"
```

Press `E` to select Emby. Emby opens directly in **By Artist / Album** mode. The artists are in alphabetical order. Select an artist to open the albums of that artist. Select an album to open its songs.

Press `N` while you browse Emby to switch to **By Album** or **By Artist** for the current session. The next launch returns to **By Artist / Album**.

## How it works

cliamp authenticates with an API key or the supplied username and password. It resolves the active Emby user and lists the music library views. It reads the albums of each view in pages of 500 and derives an alphabetical artist index from them. Then it gets the tracks for the selected album. Playback uses Emby's authenticated download endpoint and streams through the cliamp HTTP pipeline.

## Known limitations

- **Token-based access**: Store the API key safely.
- **API key user selection**: Emby API keys apply to the server and have no "current user". Without `user`, cliamp selects the first user returned by `/Users`. This is correct for a single-user server. On a multi-user server, set `user_id` in `[emby]` to select an account.
