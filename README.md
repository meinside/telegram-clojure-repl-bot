# telegram-bot-repl

A Telegram bot for using REPL, built with golang.

![telegram-bot-repl_screenshot_20191110](https://user-images.githubusercontent.com/185988/68541027-ab035700-03dd-11ea-9151-ed1b811e2c8b.jpg)

It connects to an existing PREPL connection, or launch a new PREPL and communicate with it.

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
$ sudo launchctl load /Library/LaunchDaemons/telegram-bot-repl.plist
```

and unload with:

```bash
$ sudo launchctl unload /Library/LaunchDaemons/telegram-bot-repl.plist
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

