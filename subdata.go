package main

import (
	"fmt"
	"context"
	"encoding/base64"
	"io/ioutil"
	"os"
	"os/exec"
	"path"
	"regexp"
	"strings"

	"cloud.google.com/go/pubsub"
	"github.com/sirupsen/logrus"
)

type subData struct {
	Sub          string
	Topic        string
	Command      subCommand
	PassDataVia  dataVia
	Ifs          []*subIf
	Tests        []*subDataTest
	Cancellation context.CancelFunc
}

type dataVia string

const (
	dataViaNone dataVia = "none"
	dataViaPipe dataVia = "pipe"
	dataViaFile dataVia = "file"
	dataViaVar  dataVia = "var"
)

type subIf struct {
	FieldType subIfField
	FieldName string
	Pattern   *regexp.Regexp
	Command   subCommand
	Then      []*subIf
}

type subIfField int

const (
	subIfFieldNone subIfField = iota
	subIfFieldMetaKey
)

type subDataTest struct {
	Data    string            `yaml:"data"`
	Meta    map[string]string `yaml:"meta"`
	Message *pubsub.Message   `yaml:"-"`
}

type subCommand []string

// ---

func (sd *subData) receiveMessage(ctx context.Context, msg *pubsub.Message, log *logrus.Entry) bool {
	log.Debug("New message ", len(msg.Data), " B length and ", len(msg.Attributes), " attrs")

	// command args replaces
	commandReplaces := make(map[string]string)
	commandReplaces["GSUB_SUB"] = sd.Sub
	commandReplaces["GSUB_TOPIC"] = sd.Topic
	for metak, metav := range msg.Attributes {
		commandReplaces["GSUB_META_"+strings.ReplaceAll(metak, " ", "_")] = metav
	}

	// msg data => file
	var dataFile string
	if sd.PassDataVia == dataViaFile {
		dataFile = path.Join(os.TempDir(), "gpubsub_message_"+msg.ID)
		log.Trace("Writing data file ", dataFile)
		if err := ioutil.WriteFile(dataFile, msg.Data, 0600); err != nil {
			log.Error("Failed to write data file: ", err)
			return false
		}
		defer func() {
			log.Trace("Removing data file")
			os.Remove(dataFile)
		}()
		commandReplaces["GSUB_DATA"] = dataFile
	}

	// msg data => var
	if sd.PassDataVia == dataViaVar {
		commandReplaces["GSUB_DATA"] = base64.StdEncoding.EncodeToString(msg.Data)
	}

	// msg data => pipe
	var passBytesViaPipe []byte
	if sd.PassDataVia == dataViaPipe {
		passBytesViaPipe = msg.Data
	}

	// ---

	// perform root command
	if !sd.Command.empty() {
		sd.Command.perform(commandReplaces, passBytesViaPipe, log.WithField("cmd", "root"))
	}

	// ifs
	if len(sd.Ifs) > 0 {
		for i, iff := range sd.Ifs {
			iff.eval(msg, []int{i}, func(iff *subIf, index []int) {
				log.Debug("If at ", index, " triggered")
				iff.Command.perform(commandReplaces, passBytesViaPipe, log.WithField("cmd", fmt.Sprintf("if%v", index)))
			})
		}
	}

	return true
}

func (iff *subIf) eval(msg *pubsub.Message, index []int, cbk func(*subIf, []int)) {
	value := ""
	switch iff.FieldType {
	case subIfFieldMetaKey:
		value = msg.Attributes[iff.FieldName]
	default:
		return
	}
	if !iff.Pattern.MatchString(value) {
		return
	}
	cbk(iff, index)
	for i, v := range iff.Then {
		v.eval(msg, append(append(index[:0:0], index...), i), cbk)
	}
}

func (sc subCommand) empty() bool {
	return len(sc) == 0 || strings.TrimSpace(sc[0]) == ""
}

func (sc subCommand) perform(replaces map[string]string, passViaPipe []byte, log *logrus.Entry) bool {
	if sc.empty() {
		return true
	}

	// command and args
	cmd := strings.TrimSpace(sc[0])
	cmdArgs := make([]string, len(sc)-1)
	if len(cmdArgs) > 0 {
		arr := make([]string, 2*len(replaces))
		for k, v := range replaces {
			arr = append(arr, k, v)
		}
		replacer := strings.NewReplacer(arr...)
		for i, v := range sc[1:] {
			cmdArgs[i] = replacer.Replace(v)
		}
	}

	log.Info("Performing ", cmd, cmdArgs)
	command := exec.Command(cmd, cmdArgs...)

	// pass data through pipe
	if len(passViaPipe) > 0 {
		stdin, err := command.StdinPipe()
		if err != nil {
			log.Error("Failed to open pipe: ", err)
			return false
		}
		defer stdin.Close()
		_, err = stdin.Write([]byte(base64.StdEncoding.EncodeToString(passViaPipe)))
		if err != nil {
			log.Error("Failed to write to pipe: ", err)
			return false
		}
	}

	// perform
	output, err := command.CombinedOutput()
	if err != nil {
		log.Error("Error: ", err)
		log.Error("Output: ", string(output))
	} else {
		log.Info("Success")
		log.Debug("Output: ", string(output))
	}
	return true
}
