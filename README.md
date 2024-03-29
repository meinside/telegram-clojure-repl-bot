# telegram-clojure-repl-bot

A Telegram bot for using Clojure REPL, built with golang.

![telegram-bot-repl_screenshot_20191110](https://user-images.githubusercontent.com/185988/68541027-ab035700-03dd-11ea-9151-ed1b811e2c8b.jpg)

It connects to an existing PREPL connection, or launches a new PREPL and communicates with it.

## 1. Install

```bash
$ go install github.com/meinside/telegram-clojure-repl-bot@latest
```

## 2. Configure

```bash
$ git clone https://github.com/meinside/telegram-clojure-repl-bot.git
$ cp telegram-clojure-repl-bot/config.json.sample /path/to/your/config.json
$ vi /path/to/your/config.json
```

and change values to yours:

```json
{
	"api_token": "0123456789:abcdefghijklmnopqrstuvwyz-x-0a1b2c3d4e",
	"clojure_bin_path": "/usr/local/bin/clojure",
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

## 3. Run

Execute the installed binary with the path to your config file:

```bash
$ telegram-clojure-repl-bot /path/to/your/config.json
```

## 4. Run as a service

### A. Systemd on Linux

```bash
$ sudo cp telegram-clojure-repl-bot/systemd/telegram-clojure-repl-bot.service /etc/systemd/system/
$ sudo vi /etc/systemd/system/telegram-clojure-repl-bot.service
```

and edit **User**, **Group**, **WorkingDirectory** and **ExecStart** values.

It will launch automatically on boot with:

```bash
$ sudo systemctl enable telegram-clojure-repl-bot.service
```

and will start/stop manually with:

```bash
$ sudo systemctl start telegram-clojure-repl-bot.service
$ sudo systemctl stop telegram-clojure-repl-bot.service
```

### B. Launchd on macOS

```bash
$ sudo cp telegram-clojure-repl-bot/launchd/telegram-clojure-repl-bot.plist /Library/LaunchDaemons/
$ sudo vi /Library/LaunchDaemons/telegram-clojure-repl-bot.plist
```

and edit values:

```
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple Computer//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
	<key>Label</key>
	<string>telegram-clojure-repl-bot</string>
	<key>ProgramArguments</key>
	<array>
		<string>/path/to/installed/telegram-clojure-repl-bot</string>
		<string>/path/to/your/config.json</string>
	</array>
	<key>UserName</key>
	<string>user_name</string>
	<key>RunAtLoad</key>
	<true/>
	<key>KeepAlive</key>
	<true/>
</dict>
</plist>
```

Now load it with:

```bash
$ sudo launchctl load /Library/LaunchDaemons/telegram-clojure-repl-bot.plist
```

and unload with:

```bash
$ sudo launchctl unload /Library/LaunchDaemons/telegram-clojure-repl-bot.plist
```

## 999. Known Issues

### A. Stdin Problem with Systemd

When running with systemd, the PREPL launched by this bot stops immediately because it is an interactive shell-like application.

So I put `StandardInput` and `TTYPath` properties in the Service section in systemd .service file:

```
StandardInput=tty
TTYPath=/dev/tty49
```

If these would be a problem for you, change them or launch your PREPL manually.

### B. Systemd Service Failure with ASDF

When running with java/clojure installed with [asdf](http://asdf-vm.com/), `JAVA_HOME` must be specified in the `Environment` section of the systemd .service file:

```
# example
Environment="JAVA_HOME=/home/ubuntu/.asdf/installs/java/zulu-17.32.13"
```

## License

MIT

