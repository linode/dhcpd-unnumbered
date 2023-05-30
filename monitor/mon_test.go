package monitor

import (
	"fmt"
	"net"
	"os/user"
	"regexp"
	"sync"
	"testing"

	ll "github.com/sirupsen/logrus"
	"github.com/stretchr/testify/assert"
	"github.com/vishvananda/netlink"
	"github.com/vishvananda/netns"
)

// Wrap the netlink packages and just expose creation and clean up of VRFs.
type myNetlink struct {
	ns netns.NsHandle
	nh *netlink.Handle

	interfaces map[netlink.Link]bool
}

func newNetlink() (*myNetlink, error) {
	var err error
	var nsHandle netns.NsHandle

	nsHandle, err = netns.Get()
	if err != nil {
		return nil, fmt.Errorf("getting namespace: %v", err)
	}
	handle, err := netlink.NewHandleAt(nsHandle)
	if err != nil {
		return nil, fmt.Errorf("constructing handle: %v", err)
	}
	nh := &myNetlink{
		ns:         nsHandle,
		nh:         handle,
		interfaces: make(map[netlink.Link]bool),
	}
	return nh, nil
}

func (nh *myNetlink) createVRF(name string) (netlink.Link, error) {
	attrs := netlink.NewLinkAttrs()
	attrs.Flags |= net.FlagUp
	vrf := &netlink.Vrf{
		LinkAttrs: attrs,
		Table:     42,
	}
	vrf.Name = name

	err := nh.nh.LinkAdd(vrf)
	nh.interfaces[vrf] = true
	return vrf, err
}

func (nh *myNetlink) deleteVRF(l netlink.Link) error {
	err := nh.nh.LinkDel(l)
	delete(nh.interfaces, l)
	return err
}

func (nh *myNetlink) cleanup() error {
	// Delete any left over interfaces
	for k := range nh.interfaces {
		nh.deleteVRF(k)
	}

	// Close namespace handle
	err := nh.ns.Close()
	if err != nil {
		return fmt.Errorf("closing namespace: %v", err)
	}

	return nil
}

func skipUnlessRoot(t *testing.T) {
	u, err := user.Current()
	if err != nil {
		// Far fetched...
		t.Skip("Skipping as we cannot determine user!", err)
	}
	if u.Uid != "0" {
		t.Skip("Skipping as test requires root")
	}
}

// Bring up and take down some interfaces, but only some have names matching our
// regex
func TestUpDown(t *testing.T) {
	skipUnlessRoot(t)
	// This is not really great, because the test will just hang if it doesn't
	// work.

	ll.SetLevel(ll.DebugLevel)
	// Create a namespace handle
	myNL, err := newNetlink()
	t.Cleanup(func() { myNL.cleanup() })
	assert.Nil(t, err)

	events := make(chan Event, 5)

	regex := regexp.MustCompile(".*myvrf.*")

	mon := NewNetlinkMonitor(events, regex)
	go mon.Listen()

	myvrf := make([]netlink.Link, 5)
	myvuf := make([]netlink.Link, 5)

	wg := sync.WaitGroup{}
	wg.Add(1)
	var upevent = func() {
		cnt := len(myvrf)
		defer wg.Done()

		for {
			cnt--
			// Get an event
			ev := <-events
			ll.Infof("interface=%v, type=%v", ev.Interface, ev.Type)
			// We mustn't see down events before up
			assert.True(t, ev.Type != LinkDown)
			assert.True(t, regex.Match([]byte(ev.Interface)))
			if cnt <= 0 {
				return
			}
		}
	}

	go upevent()

	// Create the interfaces we're not expecting and they should be ignored
	for n := range myvuf {
		name := fmt.Sprintf("myvuf%d", n)
		vrf, err := myNL.createVRF(name)
		assert.Nil(t, err)
		myvuf[n] = vrf
	}

	// These should not be ignored
	for n := range myvrf {
		name := fmt.Sprintf("myvrf%d", n)
		vrf, err := myNL.createVRF(name)
		assert.Nil(t, err)
		myvrf[n] = vrf
	}

	// Wait for up
	wg.Wait()

	wg.Add(1)
	var downevent = func() {
		cnt := len(myvrf)
		defer wg.Done()

		for {
			// Get an event
			ev := <-events
			cnt--
			ll.Infof("interface=%v, type=%v", ev.Interface, ev.Type)
			// Only down events
			assert.True(t, ev.Type != LinkUp)
			assert.True(t, regex.Match([]byte(ev.Interface)))
			if cnt <= 0 {
				return
			}
		}
	}

	go downevent()

	for n := range myvuf {
		myNL.deleteVRF(myvuf[n])
	}

	for n := range myvrf {
		myNL.deleteVRF(myvrf[n])
	}

	// Wait for down
	wg.Wait()

	// Done
	mon.Close()
}
