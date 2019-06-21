package main

import (
	"errors"

	kservice "github.com/kardianos/service"
)

func makeService(wd string, args []string, start func(), stop func()) (kservice.Service, error) {
	kcfg := &kservice.Config{
		Name:             "gpubsub",
		DisplayName:      "gpubsub",
		Description:      "Listens for Google Cloud Pub/Sub messages and performs specified commands",
		Arguments:        args,
		WorkingDirectory: wd,
	}
	impl := &serviceImpl{
		start: start,
		stop:  stop,
	}
	ksvc, err := kservice.New(impl, kcfg)
	if err != nil {
		return nil, err
	}
	_, err = ksvc.Logger(nil)
	if err != nil {
		return nil, err
	}
	return ksvc, nil
}

func serviceInstall(wd string, args []string) error {
	ksvc, err := makeService(wd, args, nil, nil)
	if err != nil {
		return err
	}
	return ksvc.Install()
}

func serviceUninstall() error {
	ksvc, err := makeService("", nil, nil, nil)
	if err != nil {
		return err
	}
	return ksvc.Uninstall()
}

func serviceServe(start func(), stop func()) error {
	if start == nil || stop == nil {
		return errors.New("invalid start/stop callbacks")
	}
	ksvc, err := makeService("", nil, start, stop)
	if err != nil {
		return err
	}
	return ksvc.Run()
}

func serviceInteractive() bool {
	return kservice.Interactive()
}

type serviceImpl struct {
	start func()
	stop  func()
}

// Start impl
func (p *serviceImpl) Start(s kservice.Service) error {
	go p.start()
	return nil
}

// Stop impl
func (p *serviceImpl) Stop(s kservice.Service) error {
	go p.stop()
	return nil
}
