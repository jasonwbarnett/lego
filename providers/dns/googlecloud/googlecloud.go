// Package googlecloud implements a DNS provider for solving the DNS-01
// challenge using Google Cloud DNS.
package googlecloud

import (
	"fmt"
	"log"
	"os"
	"strings"
	"time"

	"github.com/xenolf/lego/acme"

	"golang.org/x/net/context"
	"golang.org/x/oauth2/google"

	"google.golang.org/api/dns/v1"
)

var (
	// Logger is an optional custom logger.
	Logger         *log.Logger
	checkFrequency = 5
)

// logf writes a log entry. It uses Logger if not
// nil, otherwise it uses the default log.Logger.
func logf(format string, args ...interface{}) {
	if Logger != nil {
		Logger.Printf(format, args...)
	} else {
		log.Printf(format, args...)
	}
}

// DNSProvider is an implementation of the DNSProvider interface.
type DNSProvider struct {
	project string
	client  *dns.Service
}

// NewDNSProvider returns a DNSProvider instance configured for Google Cloud
// DNS. Credentials must be passed in the environment variable: GCE_PROJECT.
func NewDNSProvider() (*DNSProvider, error) {
	project := os.Getenv("GCE_PROJECT")
	return NewDNSProviderCredentials(project)
}

// NewDNSProviderCredentials uses the supplied credentials to return a
// DNSProvider instance configured for Google Cloud DNS.
func NewDNSProviderCredentials(project string) (*DNSProvider, error) {
	if project == "" {
		return nil, fmt.Errorf("Google Cloud project name missing")
	}

	client, err := google.DefaultClient(context.Background(), dns.NdevClouddnsReadwriteScope)
	if err != nil {
		return nil, fmt.Errorf("Unable to get Google Cloud client: %v", err)
	}
	svc, err := dns.New(client)
	if err != nil {
		return nil, fmt.Errorf("Unable to create Google Cloud DNS service: %v", err)
	}
	return &DNSProvider{
		project: project,
		client:  svc,
	}, nil
}

// Present creates a TXT record to fulfil the dns-01 challenge.
func (c *DNSProvider) Present(domain, token, keyAuth string) error {
	fqdn, value, ttl := acme.DNS01Record(domain, keyAuth)

	zone, err := c.getHostedZone(domain)
	if err != nil {
		return err
	}

	rec := &dns.ResourceRecordSet{
		Name:    fqdn,
		Rrdatas: []string{value},
		Ttl:     int64(ttl),
		Type:    "TXT",
	}
	change := &dns.Change{
		Additions: []*dns.ResourceRecordSet{rec},
	}

	logf("[INFO][%s][googlecloud][%s] Creating TXT record", domain, fqdn)
	chg, err := c.client.Changes.Create(c.project, zone, change).Do()
	if err != nil {
		return err
	}

	// wait for change to be acknowledged
	for chg.Status == "pending" {
		logf("[INFO][%s][googlecloud][%s] Change is pending still, will check again in %d seconds", domain, fqdn, checkFrequency)
		time.Sleep(time.Duration(checkFrequency) * time.Second)

		chg, err = c.client.Changes.Get(c.project, zone, chg.Id).Do()
		if err != nil {
			return err
		}
	}

	return nil
}

// CleanUp removes the TXT record matching the specified parameters.
func (c *DNSProvider) CleanUp(domain, token, keyAuth string) error {
	fqdn, _, _ := acme.DNS01Record(domain, keyAuth)

	zone, err := c.getHostedZone(domain)
	if err != nil {
		return err
	}

	records, err := c.findTxtRecords(zone, fqdn)
	if err != nil {
		return err
	}

	for _, rec := range records {
		change := &dns.Change{
			Deletions: []*dns.ResourceRecordSet{rec},
		}
		_, err = c.client.Changes.Create(c.project, zone, change).Do()
		if err != nil {
			return err
		}
	}
	return nil
}

// Timeout customizes the timeout values used by the ACME package for checking
// DNS record validity.
func (c *DNSProvider) Timeout() (timeout, interval time.Duration) {
	return 90 * time.Second, 2 * time.Second
}

// getHostedZone returns the managed-zone
func (c *DNSProvider) getHostedZone(domain string) (string, error) {

	zones, err := c.client.ManagedZones.List(c.project).Do()
	if err != nil {
		return "", fmt.Errorf("GoogleCloud API call failed: %v", err)
	}

	for _, z := range zones.ManagedZones {
		if strings.HasSuffix(domain+".", z.DnsName) {
			return z.Name, nil
		}
	}

	return "", fmt.Errorf("No matching GoogleCloud domain found for domain %s", domain)

}

func (c *DNSProvider) findTxtRecords(zone, fqdn string) ([]*dns.ResourceRecordSet, error) {

	recs, err := c.client.ResourceRecordSets.List(c.project, zone).Do()
	if err != nil {
		return nil, err
	}

	found := []*dns.ResourceRecordSet{}
	for _, r := range recs.Rrsets {
		if r.Type == "TXT" && r.Name == fqdn {
			found = append(found, r)
		}
	}

	return found, nil
}
