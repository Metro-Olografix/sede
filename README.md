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

il backend è un semplice web server in Go, espone:

 - `GET /status`: risponde `true` o `false`
 - `POST /toggle`: cambia lo stato della sede e ritorna il nuovo stato
 - `GET /stats`: ritorna le statistiche orario con probabilità di trovare la sede aperta o chiusa in base allo storico
 - `GET /ui`: attiva solo se `DEBUG=true`

per lanciarlo in locale:

```shell
git clone git@github.com:Metro-Olografix/sede.git

cd sede/backend

docker build -t sede .

docker run -p 8080:8080 -e DEBUG=true -v ./database:/app/database sede
```