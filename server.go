package main

import (
	ll "github.com/sirupsen/logrus"
)

type Srv struct {
	l *Listener
}

// NewServer instantiates a server type
func NewServer() (*Srv, error) {
	ll.Infoln("Starting DHCPv4 server")

	l, err := NewListener()
	if err != nil {
		return nil, err
	}

	s := Srv{
		l: l,
	}

	return &s, nil
}

// Serve starts the listener and blocks till error returns
func (s *Srv) Serve() error {
	return s.l.Listen()
}
