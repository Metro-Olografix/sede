# sede

sistema pubblico delle presenze sviluppato per la nuova sede della [Metro Olografix](https://olografix.org).

questo progetto è ispirato a [BITS](https://bits.poul.org/) ([Gitlab](https://gitlab.poul.org/project/b4/bits-server)).

## progetto

il progetto è diviso in due parti:

### hardware

 - ESP32 (S3 consigliato), correttamente testato su [Xiao ESP32-S3](https://wiki.seeedstudio.com/xiao_esp32s3_getting_started/).
 - [RGB LED Light Switch momentaneo](https://it.aliexpress.com/item/1005003238777047.html) / 3-6V
 - 3 resistenze da 330ohm

_Schematic:_

![schematic](https://github.com/Metro-Olografix/sede/blob/main/hardware/schematic.png?raw=true)

il firmware è sviluppato utilizzando il framework [ESPHome](https://esphome.io/) ed è modificabile [qui](https://github.com/Metro-Olografix/sede/blob/main/hardware/config/pulsante-sede.yaml).

per flashare il firmware sul proprio ESP32 è necessario avviare ESPHome in locale o su una istanza remota, per lanciarlo in locale:

```shell
git clone git@github.com:Metro-Olografix/sede.git

cd sede/hardware

docker compose up -d
```

ora, aprire [http://localhost:6052](http://localhost:6052) sul proprio browser e sarà già disponibile `pulsante-sede.yaml` nella schermata iniziale.

### backend

il backend è un semplice web server in Go, multi-tenant: una sola
istanza serve N sedi tramite prefisso di path `/s/{slug}/...`. Le rotte
"bare" restano come alias della sede di default (`DEFAULT_SPACE_SLUG`,
`pescara`) per compatibilità con i client già deployati (pulsante
ESP32, MCP server).

Endpoint per ciascuna sede:

 - `GET /s/{slug}/status` (alias: `GET /status`): risponde `true` o `false`
 - `POST /s/{slug}/toggle` (alias: `POST /toggle`): cambia lo stato. Richiede `X-API-KEY` della sede.
 - `GET /s/{slug}/stats` (alias: `GET /stats`): statistiche orarie
 - `GET /s/{slug}/spaceapi.json` (alias: `GET /spaceapi.json`): metadati SpaceAPI v15
 - `GET /s/{slug}/ui` (alias: `GET /ui`): heatmap, attiva solo se `DEBUG=true`

Le sedi sono dichiarate in `config/spaces.yaml` (vedi
`backend/deploy/spaces.example.yaml`): slug, nome, coordinate, API key
(supporta `$VAR`), chat/thread Telegram, metadati SpaceAPI. Il file è
caricato al boot e fa upsert sulle righe del DB per slug.

per lanciarlo in locale:

```shell
git clone git@github.com:Metro-Olografix/sede.git

cd sede/backend

docker build -t sede .

docker run -p 8080:8080 -e DEBUG=true -v ./database:/app/database sede
```

### server MCP

perchè non dare la possibilità agli LLM di sapere se la sede è aperta o chiusa?

#### configurare Claude Desktop

aggiungere dentro `claude_desktop_config.json`:

```json
{
  "mcpServers": {
    "mx-sede": {
      "command": "docker",
      "args": [
        "run",
        "-i",
        "--rm",
        "ghcr.io/metro-olografix/sede/mcp:latest"
      ]
    }
  }
}
```

#### sviluppo locale

https://modelcontextprotocol.io/quickstart/server#set-up-your-environment