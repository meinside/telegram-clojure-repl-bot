# telegram-bot-repl

A Telegram bot for using REPL, built with golang.

![screen shot 2017-01-17 at 17 36 13](https://cloud.githubusercontent.com/assets/185988/22013289/9508932e-dcdb-11e6-8429-abf51a74bd83.png)

## 1. Install

```bash
$ go get -u github.com/meinside/telegram-bot-repl
```

## 2. Configure

```bash
$ cd $GOPATH/src/github.com/meinside/telegram-bot-repl
$ cp config.json.sample config.json
$ vi config.json
```

and change values to yours:

```json
{
	"api_token": "0123456789:abcdefghijklmnopqrstuvwyz-x-0a1b2c3d4e",
	"lein_exec_path": "/usr/local/bin/lein",
	"repl_host": "localhost",
	"repl_port": 8888,
	"allowed_ids": [
		"telegram_id_1",
		"telegram_id_2"
	],
	"monitor_interval": 1,
	"is_verbose": false
}
```

## 3. Build and run

Build,

```bash
$ cd $GOPATH/src/github.com/meinside/telegram-bot-repl
$ go build
```

and run:

```bash
$ ./telegram-bot-repl
```

## 4. Run as a service

### A. Systemd on Linux

```bash
$ cd $GOPATH/src/github.com/meinside/telegram-bot-repl/systemd
$ sudo cp telegram-bot-repl.service /lib/systemd/system/
$ sudo vi /lib/systemd/system/telegram-bot-repl.service
```

and edit **User**, **Group**, **WorkingDirectory** and **ExecStart** values.

It will launch automatically on boot with:

```bash
$ sudo systemctl enable telegram-bot-repl.service
```

and will start/stop manually with:

```bash
$ sudo systemctl start telegram-bot-repl.service
$ sudo systemctl stop telegram-bot-repl.service
```

### B. Launchd on macOS

```bash
$ cd $GOPATH/src/github.com/meinside/telegram-bot-repl/launchd
$ sudo cp telegram-bot-repl.plist /Library/LaunchDaemons/telegram-bot-repl.plist
$ sudo vi /Library/LaunchDaemons/telegram-bot-repl.plist
```

and edit values:

```
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple Computer//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
	<key>Label</key>
	<string>telegram-bot-repl</string>
	<key>ProgramArguments</key>
	<array>
		<string>/path/to/telegram-bot-repl/telegram-bot-repl</string>
	</array>
	<key>RunAtLoad</key>
	<true/>
	<key>KeepAlive</key>
	<true/>
</dict>
</plist>
```

Now load it with:

```bash
$ sudo launchctl load /Library/LaunchDaemons/telegram-bot-repl.plist
```

and unload with:

```bash
$ sudo launchctl unload /Library/LaunchDaemons/telegram-bot-repl.plist
```

## License

MIT

