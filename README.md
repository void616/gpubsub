gpubsub pulls Google Cloud Pub/Sub messages using [Pull](https://cloud.google.com/pubsub/docs/pull) strategy and then performs a command passing message's data and attributes. \
Subscription **must exist** on Google Cloud side.

## How

gpubsub acts as daemon. Next directory structure is required (for instance):
```sh
/opt
  /gpubsub
    /scripts    # optional, directory with shell scripts (your actions)
    gpubsub     # executable
	subs.yaml   # config describes subscriptions and what scripts to run
    creds.json  # optional, contains Google Cloud credentials to interact with Pub/Sub
```

Config example (details below):
```yaml
project: myprojectid-1337              # Your Google Cloud project ID
subs:                                  # List of subscriptions to listen to
  - name: my-subscription-name         # Subscription as named in Google Cloud Console and paste name here
    disable: no                        # Listen this subscription
    data: none                         # Don't pass messages payload into scripts (see options below)
    cmd:                               # Do nothing on received message,
    if:                                #   instead of that check preconditions:
      - metakey: server                # IF message's metadata key named "server"
		equal: ^staging$               # equal to "staging" (RE2 here)
		cmd:                           # THEN do nothing (empty command)
        then:                          # AND
          - metakey: app                                       # IF message's metadata with key "app"
            equal: ^frontend$                                  # equal to "frontend"
            cmd: [sh, /opt/gpubsub/scripts/update-frontend.sh] # THEN run the script to update my frontend server
          - metakey: app                                       # OR IF message's metadata with key "app"
            equal: ^backend$                                   # equal to "backend"
            cmd: [sh, /opt/gpubsub/scripts/update-backend.sh]  # THEN run the script to update my backend server
```

## Testing

Add next to your config:
```yaml
project: myprojectid-1337
subs:
  - name: my-subscription-name
    tests:                        # Next test messages will be sent to the subscription listener
      - data: test1               # Message's payload: text or Base64-encoded string
        meta:                     # Message's metadata k/v
          server: staging
          app: frontend
	  - data: test2
	    meta:
		  server: production
		  app: another-app
```
Subscription listener will not be launched. Test messages will be sent to it instead.\
Then test it with a real messages: navigate to [Pub/Sub](https://console.cloud.google.com/cloudpubsub) and send a test message under your topic.

## Pass message to the script

Next strings will be replaced under `cmd` field of the subscription (as well as cmds within IFs):
| Variable        | Replacement |
| ---             | ---     |
| `GSUB_SUB`      | Subscription name |
| `GSUB_TOPIC`    | Subscription's topic name |
| `GSUB_META_XXX` | Message's metadata under XXX key (for example: "my key" => "my_key" => "GSUB_META_my_key") |
| `GSUB_DATA`     | Message's payload (as Base64 or file name, depends on `data` field, see details below) |

Subscription's `data` field defines how message's payload will be passed to the performing script:
| Value  | Description |
| ---    | --- |
| `var`  | `GSUB_DATA` variable will be replaced with Base64-encoded string of the payload |
| `pipe` | Payload will be passed through the pipe as Base64-encoded string (unavailable on Windows) |
| `file` | `GSUB_DATA` variable will be replaced with a temp file path, containing raw bytes of the payload |
| `none` | Nothing will be passed (`GSUB_DATA` is unchanged) |

Example:
```yaml
project: myprojectid-1337
subs:
  - name: sub1
    data: var                     # var
	cmd: [sh, -c, echo GSUB_DATA] # GSUB_DATA replaced with Base64 string
  - name: sub2
    data: pipe                    # pipe
	cmd: [sh, script.sh]          # read Base64 string within script
  - name: sub3
    data: file                    # pipe
	cmd: [sh, -c, echo GSUB_DATA] # GSUB_DATA replaced with temp filename
  - name: sub4
    data: none                    # nothing
    cmd: [sh, -c, echo GSUB_DATA] # GSUB_DATA still GSUB_DATA
```

## Build

```sh
export GOOS=linux &&
export GOARCH=amd64 &&
go build -ldflags '-s -w' -o gpubsub *.go
```

## Daemonizing

Install:
```sh
./gpubsub --creds=/path/to/creds.json install
```

Uninstall:
```sh
./gpubsub uninstall
# systemd cleanups (I'm using CentOS)
systemctl daemon-reload
systemctl reset-failed
```

## TODO

- Decoupling and unit tests
- Benchmarks