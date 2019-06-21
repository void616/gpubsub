package main

import (
	"context"
	"flag"
	"io/ioutil"
	"os"
	"os/signal"
	"path"
	"runtime"
	"strings"
	"sync"
	"syscall"

	"cloud.google.com/go/pubsub"
	"github.com/sirupsen/logrus"
	"google.golang.org/api/option"
)

var (
	argClientCreds   = flag.String("creds", "", "Optional credentials Json file path (from Google Cloud console)")
	argSubscriptions = flag.String("subs", "subs.yaml", "Subscriptions Yaml file path")
	argVerbose       = flag.Bool("verbose", false, "Verbose output for debugging")
	argLogFile       = flag.String("logto", "", "File path where to log")
)

var (
	log       *logrus.Logger
	projectID string
	subz      map[string]*subData
	stopOnce  = sync.Once{}
)

func main() {

	log = logrus.New()
	log.Formatter.(*logrus.TextFormatter).ForceColors = true
	log.Formatter.(*logrus.TextFormatter).DisableColors = false
	log.Formatter.(*logrus.TextFormatter).FullTimestamp = false
	log.Level = logrus.InfoLevel
	log.Out = os.Stdout

	flag.Parse()

	wd := strings.Trim(
		path.Dir(
			strings.Replace(os.Args[0], `\`, `/`, -1),
		), "/",
	) + `/`
	if err := os.Chdir(wd); err != nil {
		log.Fatal("Failed to change working dir to ", wd)
	}

	// args
	if len(flag.Args()) > 0 {
		if len(flag.Args()) > 1 {
			log.Fatal("Invalid number of arguments")
		}
		switch flag.Arg(0) {
		case "install":
			if !serviceInteractive() {
				log.Fatal("Install in interactive mode")
			}
			iargs := make([]string, 0)
			flag.Visit(func(flg *flag.Flag) {
				iargs = append(iargs, "--"+flg.Name+"="+flg.Value.String())
			})
			if err := serviceInstall(wd, iargs); err != nil {
				log.Fatal("Failed to install: ", err)
			}
			log.Info("Success")
			return
		case "uninstall":
			if !serviceInteractive() {
				log.Fatal("Uninstall in interactive mode")
			}
			if err := serviceUninstall(); err != nil {
				log.Fatal("Failed to uninstall: ", err)
			}
			log.Info("Success")
			return
		default:
			log.Fatal("Unknown argument")
		}
	}

	// log tweaks
	{
		if *argVerbose {
			log.Level = logrus.TraceLevel
		}
		if runtime.GOOS == "windows" && !serviceInteractive() {
			*argLogFile = "gpubsub.log"
		}
		if *argLogFile != "" {
			file, err := os.OpenFile(*argLogFile, os.O_CREATE|os.O_WRONLY, 0666)
			if err != nil {
				log.Fatal("Failed to write log to file: ", err)
			}
			defer file.Close()
			log.Out = file
			log.Formatter.(*logrus.TextFormatter).ForceColors = false
			log.Formatter.(*logrus.TextFormatter).DisableColors = true
			log.Formatter.(*logrus.TextFormatter).FullTimestamp = true
		}
	}

	if serviceInteractive() {
		onStart()
		return
	}

	if err := serviceServe(onStart, onStop); err != nil {
		log.Fatal("Failed to start service: ", err)
	}
}

func onStart() {

	// subs from yaml config
	var testing bool
	{
		log.Debug("Reading subscriptions file: ", *argSubscriptions)
		b, err := ioutil.ReadFile(*argSubscriptions)
		if err != nil {
			log.Fatal("Failed to read subscriptions: ", err)
		}
		projID, subzMap, hasTests, err := parseSubsConfig(b)
		if err != nil {
			log.Fatal("Failed to parse subscriptions: ", err)
		}
		projectID = projID
		subz = subzMap
		testing = hasTests
		log.Info("Project ", projID, ", ", len(subz), " subscriptions")
	}

	// optional creds
	var clientOpts []option.ClientOption
	if *argClientCreds != "" {
		log.Warning("Using credentials from file ", *argClientCreds)
		b, err := ioutil.ReadFile(*argClientCreds)
		if err != nil {
			log.Fatal("Failed to read credentials: ", err)
		}
		clientOpts = append(clientOpts, option.WithCredentialsJSON(b))
	}

	// client
	var client *pubsub.Client
	ctx := context.Background()
	if !testing {
		log.Debug("Setting up client")
		c, err := pubsub.NewClient(ctx, projectID, clientOpts...)
		if err != nil {
			log.Fatal("Failed to create pub/sub client: ", err)
		}
		defer c.Close()
		client = c
	}

	// check subscriptions
	if !testing {
		log.Debug("Checking subscriptions")
		for _, v := range subz {
			sub := client.Subscription(v.Sub)
			ok, err := sub.Exists(ctx)
			if err != nil {
				log.Fatal("Failed to check subscription ", v.Sub, ":", err)
			}
			if !ok {
				log.Fatal("Subscription ", v.Sub, " does not exist. Create it first in Google Cloud console")
			}

			scfg, err := sub.Config(ctx)
			if err != nil {
				log.Fatal("Failed to get subscription ", v.Sub, " config: ", err)
			}
			v.Topic = scfg.Topic.ID()
		}
	}

	wg := sync.WaitGroup{}
	for _, v := range subz {
		logsub := log.WithField("sub", v.Sub)

		subctx, cancel := context.WithCancel(ctx)
		v.Cancellation = cancel

		if testing {
			for i, test := range v.Tests {
				logsub.Trace("Test #", i+1)
				logmsg := log.WithField("msg", test.Message.ID)
				v.receiveMessage(subctx, test.Message, logmsg)
			}
			continue
		}

		wg.Add(1)
		go func(ctx context.Context, subdata *subData, log *logrus.Entry) {
			defer wg.Done()
			defer onStop()
			defer log.Trace("Unsubscribed")
			log.Trace("Subscribed")
			sub := client.Subscription(subdata.Sub)
			if err := sub.Receive(ctx, func(ctx context.Context, msg *pubsub.Message) {
				logmsg := log.WithField("msg", msg.ID)
				if subdata.receiveMessage(ctx, msg, logmsg) {
					msg.Ack()
				} else {
					msg.Nack()
				}
			}); err != nil {
				log.Error("Failed to receive message: ", err)
			}
		}(subctx, v, logsub)
	}

	go func() {
		sigchan := make(chan os.Signal, 1)
		signal.Notify(sigchan, syscall.SIGINT, syscall.SIGTERM)
		<-sigchan
		onStop()
	}()

	wg.Wait()
	log.Info("Stopped")
}

func onStop() {
	stopOnce.Do(func() {
		log.Info("Cancelling all subscriptions...")
		for _, v := range subz {
			if v.Cancellation != nil {
				v.Cancellation()
			}
		}
	})
}
