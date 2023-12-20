package options

import (
	"net"
	"testing"

	ll "github.com/sirupsen/logrus"
	"github.com/stretchr/testify/assert"
)

func TestNoFile(t *testing.T) {
	log := ll.NewEntry(ll.StandardLogger())
	filepath := "/does-not-exist.txt"
	options, err := Load(log, filepath)

	assert.Nil(t, err)
	assert.True(t, len(options.IPv4) == 0, "IP list not empty")
	assert.Nil(t, options.Gateway, "IP not empty")
	assert.Nil(t, options.Hostname, "Hostname not empty")
	assert.Nil(t, options.Domainname, "Domainname not empty")
	assert.Nil(t, options.PvtIPs, "PvtIPs not empty")
	assert.Nil(t, options.Tftp, "Tftp not empty")
}

func TestParse(t *testing.T) {
	json := `
{
  "IPv4":       ["1.1.1.1", "invalid-will-be-skipped", "2.2.2.2"],
  "hostname":   "myhostname",
  "domainname": "domain",
  "gateway":    "1.2.3.4",
  "pvtips" :    "192.168.1.0/24",
  "tftp":       "3.4.5.6"
}
`
	log := ll.NewEntry(ll.StandardLogger())

	options, err := parse(log, []byte(json))
	assert.Nil(t, err, "Failed to load options")

	assert.Equal(t, 2, len(options.IPv4))
	assert.Equal(t, "1.1.1.1", options.IPv4[0].String(), "Bad first IP")
	assert.Equal(t, "2.2.2.2", options.IPv4[1].String(), "Bad second IP")
	assert.Equal(t, "myhostname", *options.Hostname, "Bad Hostname")
	assert.Equal(t, "domain", *options.Domainname, "Bad Domainname")
	assert.Equal(t, "1.2.3.4", options.Gateway.To4().String(), "Bad Gateway")
	assert.Equal(t, "192.168.1.0", options.PvtIPs.IP.String(), "Bad PvtIPs")
	mask := net.IPv4Mask(255, 255, 255, 0)
	assert.Equal(t, mask.String(), options.PvtIPs.Mask.String(), "Bad PvtIPs")
	assert.Equal(t, "3.4.5.6", options.Tftp.String(), "Bad Tftp")
}
