package main

import (
	"encoding/base64"
	"errors"
	"fmt"
	"regexp"
	"runtime"
	"strings"
	"time"

	"cloud.google.com/go/pubsub"
	yaml "gopkg.in/yaml.v2"
)

type subsConfig struct {
	// ProjectID is Google Cloud project ID
	ProjectID string `yaml:"project"`
	// Subscriptions are subscriptions settings
	Subscriptions []struct {
		// Name is a name of subscription as given in Google Cloud console
		Name string `yaml:"name"`
		// Disable is flag to skip this subscription while reading the config
		Disable bool `yaml:"disable"`
		// Command is a command with args to perfrom
		Command []string `yaml:"cmd"`
		// DataVia is a method of how the message's data will be passed to the performing command:
		// `var` - GSUB_DATA will be replaced with Base64-encoded string.
		// `pipe` - through the pipe as Base64-encoded string (unavailable on Windows).
		// `file` - GSUB_DATA will be replaced with a temp file path, containing raw bytes.
		// `none` - nothing will be passed.
		// Default is `var`
		DataVia string `yaml:"data"`
		// Ifs is an array of filters and commands
		Ifs []*ifConfig `yaml:"if"`
		// Test is a test data
		Tests []*subDataTest `yaml:"tests"`
	} `yaml:"subs"`
}

type ifConfig struct {
	// MetaKey specifies message's metadata key that should be evaluated
	MetaKey string `yaml:"metakey"`
	// Equal contains RE2 pattern that will be matched against input value (metadata value, for instance)
	Equal string `yaml:"equal"`
	// Command is a command with args to perfrom
	Command []string `yaml:"cmd"`
	// Then contains nested ifs
	Then []*ifConfig `yaml:"then"`
}

// parseSubsConfig reads subs.yaml content
func parseSubsConfig(b []byte) (project string, subz map[string]*subData, hasTests bool, err error) {
	project = ""
	subz = make(map[string]*subData)
	hasTests = false
	err = nil

	cfg := subsConfig{}
	if err = yaml.Unmarshal(b, &cfg); err != nil {
		err = errors.New("failed to unmarshal yaml: " + err.Error())
		return
	}

	project = cfg.ProjectID
	for _, v := range cfg.Subscriptions {
		// skip
		if v.Disable {
			continue
		}

		// check sub name
		v.Name = strings.TrimSpace(v.Name)
		if len(v.Name) < 3 || len(v.Name) > 255 {
			err = errors.New("invalid subscription name: " + v.Name)
			return
		}

		// message's data passing method
		var passDataVia = dataViaVar
		switch v.DataVia {
		case "", "var":
			passDataVia = dataViaVar
		case "pipe":
			passDataVia = dataViaPipe
			if runtime.GOOS == "windows" {
				passDataVia = dataViaVar
			}
		case "file":
			passDataVia = dataViaFile
		case "none":
			passDataVia = dataViaNone
		default:
			err = errors.New("invalid data passing method: " + v.DataVia)
			return
		}

		// validate ifs
		ifs := make([]*subIf, 0)
		{
			fail := false
			for i, v := range v.Ifs {
				iff, ok := validateSubsConfigIfs(v, []int{i})
				ifs = append(ifs, iff)
				if !ok {
					fail = true
				}
			}
			if fail {
				log.Fatal("Failed to validate ifs section")
			}
		}

		// tests data
		if len(v.Tests) > 0 {
			hasTests = true
			for testi, test := range v.Tests {
				data := []byte(test.Data)
				if b, err := base64.StdEncoding.DecodeString(test.Data); err == nil {
					data = b
				}
				msg := &pubsub.Message{
					ID:          fmt.Sprintf("%v_test_%v", v.Name, testi),
					Data:        data,
					Attributes:  test.Meta,
					PublishTime: time.Now().UTC(),
				}
				test.Message = msg
			}
		}

		// duplicate
		if _, ok := subz[v.Name]; ok {
			err = errors.New("duplicate subscription: " + v.Name)
			return
		}

		subz[v.Name] = &subData{
			Sub:          v.Name,
			Topic:        "",
			Command:      v.Command,
			PassDataVia:  passDataVia,
			Ifs:          ifs,
			Tests:        v.Tests,
			Cancellation: nil,
		}
	}

	if len(subz) == 0 {
		err = errors.New("empty subscriptions list")
		return
	}
	return
}

func validateSubsConfigIfs(v *ifConfig, index []int) (iff *subIf, gut bool) {
	iff = nil
	gut = true

	// value source
	fieldType := subIfFieldNone
	fieldName := ""
	switch {
	case v.MetaKey != "":
		fieldType = subIfFieldMetaKey
		fieldName = v.MetaKey
	default:
		log.Error("If at ", index, ": value source undefined")
		gut = false
	}

	// pattern
	if v.Equal == "" {
		log.Error("If at ", index, ": empty pattern")
		gut = false
	}
	rex, err := regexp.Compile(v.Equal)
	if err != nil {
		log.Error("If at ", index, ": invalid pattern: ", err)
		gut = false
	}

	iff = &subIf{
		FieldType: fieldType,
		FieldName: fieldName,
		Pattern:   rex,
		Command:   v.Command,
		Then:      make([]*subIf, 0),
	}

	// nested
	for i, then := range v.Then {
		subIff, good := validateSubsConfigIfs(then, append(append(index[:0:0], index...), i))
		iff.Then = append(iff.Then, subIff)
		if !good {
			gut = false
		}
	}
	return
}
