package monitor

// Use Netlink to monitor interfaces going up and down. Emit those as events.

import (
	"fmt"
	"net"
	"regexp"

	ll "github.com/sirupsen/logrus"
	"github.com/vishvananda/netlink"
)

type EventType int

const (
	LinkUp EventType = iota
	LinkDown
)

type Event struct {
	Type      EventType
	Interface string
}

type NetlinkMonitor struct {
	log      *ll.Entry
	done     chan struct{}
	matching *regexp.Regexp
	ch       chan Event // Events are emitted here
}

// NewNetlinkMonitor creates a listener for interface up/down events matching
// the given regex.
func NewNetlinkMonitor(ch chan Event, matching *regexp.Regexp) *NetlinkMonitor {
	log := ll.NewEntry(ll.StandardLogger())
	nl := &NetlinkMonitor{
		log:      log,
		done:     make(chan struct{}),
		ch:       ch,
		matching: matching,
	}
	return nl
}

// Close stops listening for events and frees resources
func (nm *NetlinkMonitor) Close() {
	close(nm.done)
}

// Listen starts lisetning for events. Blocks until Close() is called
func (nm *NetlinkMonitor) Listen() error {
	// All currently known interfaces that are up.
	interfaces := make(map[string]bool)

	updates := make(chan netlink.LinkUpdate, 10)

	// Listen for changes
	err := netlink.LinkSubscribe(updates, nm.done)
	if err != nil {
		return fmt.Errorf("unable to open netlink feed: %v", err)
	}

	links, err := netlink.LinkList()
	if err != nil {
		return fmt.Errorf("unable to get linklist: %v", err)
	}

	// Increments per event we handle
	ctr := 0

	var processLink = func(link netlink.Link) {
		ctr++
		attrs := link.Attrs()

		if attrs == nil {
			nm.log.Warnf("No attrs for interface type=%s", link.Type())
			return
		}

		ifName := attrs.Name
		state := attrs.OperState
		flags := attrs.Flags
		known := interfaces[ifName]
		log := nm.log.WithFields(ll.Fields{"intf": ifName, "ctr": ctr})

		log.WithFields(ll.Fields{"state": state, "flags": flags, "known": known}).Debug("Processing interface")

		if !nm.matching.Match([]byte(ifName)) {
			log.Debug("Skipping interface not matching regex")
			return
		}

		if interfaces[ifName] {
			// If known interface and up, ignore event
			if linkReady(attrs) {
				log.Debug("Already up")
				return
			}
			// If known interface and down, emit down event
			ev := Event{
				Type:      LinkDown,
				Interface: ifName,
			}
			delete(interfaces, ifName)
			log.Info("Interface went down")
			nm.ch <- ev
		} else {
			// New (unknown) interface
			if linkReady(attrs) {
				// If new and up, emit up event
				ev := Event{
					Type:      LinkUp,
					Interface: attrs.Name,
				}
				interfaces[attrs.Name] = true
				log.Info("New interface is up, emit up")
				nm.ch <- ev
			} else {
				// Ignore none-up events on new/unknown interfaces
				log.Debug("Ignoring event, link not ready")
			}
		}
	}

	// Synthesize Up events for existing interfaces
	nm.log.Infoln("Processing existing interfaces")
	for _, link := range links {
		processLink(link)
	}

	nm.log.Infoln("Listening for events")
	for {
		select {
		case <-nm.done:
			nm.log.Info("Netlink listener closed")
			close(nm.ch)
			return nil
		case link := <-updates:
			processLink(link)
		}
	}
}

// If this is set to false, we won't consider an interface up until we see
// traffic. This is to avoid binding the interface until it's truly "up". From
// my testing it doesn't seem necessary and I'd be concerned we might ignore
// valid interfaces. If this is set to false, tests need to be updated.
const ignoreTraffic = true

// linkReady returns true if the link is up, using the same strategy as
// rad-unnumbered.
func linkReady(l *netlink.LinkAttrs) bool {
	if l.OperState == netlink.OperUp && l.Flags&net.FlagUp == net.FlagUp {
		if ignoreTraffic || (l.Statistics != nil && l.Statistics.TxPackets > 0) {
			return true
		}
	}
	return false
}
