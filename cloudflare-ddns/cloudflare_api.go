package main

import (
	"context"
	cloudflare "github.com/cloudflare/cloudflare-go"
	"github.com/pkg/errors"
	"github.com/sirupsen/logrus"
	"io/ioutil"
	"net/http"
	"strings"
)

// GetRecord fetches the record from the Cloudflare api.
func GetRecord(ctx context.Context, api *cloudflare.API, domainName string) (*cloudflare.DNSRecord, error) {
	zoneID, err := GetZoneId(api, domainName)
	if err != nil {
		return nil, errors.Wrap(err, "could not locate zone for hostname")
	}

	// Print zone details
	dnsRecords, _, err := api.ListDNSRecords(ctx, cloudflare.ZoneIdentifier(zoneID), cloudflare.ListDNSRecordsParams{
		Name: domainName,
	})
	if err != nil {
		return nil, errors.Wrap(err, "could not locate dns record for zone")
	}

	if len(dnsRecords) != 1 {
		return nil, errors.Errorf("Expected to find a single dns record, got %d", len(dnsRecords))
	}

	// Capture the record id that we need to update.
	return &dnsRecords[0], nil
}

func GetCurrentIP(ipEndpoint string) (string, error) {
	req, err := http.NewRequest("GET", ipEndpoint, nil)
	if err != nil {
		return "", errors.Wrap(err, "could not create the request to the IP provider")
	}

	res, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", errors.Wrap(err, "could not get the current IP from the provider")
	}
	defer res.Body.Close()

	data, err := ioutil.ReadAll(res.Body)
	if err != nil {
		return "", errors.Wrap(err, "could not read the output from the provider")
	}

	// Update the IP address.
	return string(data), nil
}

// UpdateDomain updates a given domain in a zone to match the current ip address
// of the machine.
func UpdateDomain(ctx context.Context, api *cloudflare.API, domainNames, ipEndpoint string) error {
	// Get our current IP address.
	newIP, err := GetCurrentIP(ipEndpoint)
	if err != nil {
		return errors.Wrap(err, "could not get the current IP address")
	}

	logrus.WithField("ip", newIP).Info("got current IP address")

	// Split the domain names by comma, and range over them.
	splitDomainNames := strings.Split(domainNames, ",")
	for _, domainName := range splitDomainNames {
		logrus.WithField("domainName", domainName).Infoln("Checking domain")
		// Get the record in question.
		record, err := GetRecord(ctx, api, domainName)
		if err != nil {
			return errors.Wrap(err, "could not get the DNS record")
		}

		// Update the DNS record to include the new IP address.
		record.Content = newIP
		record.Proxiable = false
		record.TTL = 120

		zoneID, err := GetZoneId(api, domainName)
		if err != nil {
			return errors.Wrap(err, "could not get the DNS record")
		}

		if _, err := api.UpdateDNSRecord(ctx, cloudflare.ZoneIdentifier(zoneID), cloudflare.UpdateDNSRecordParams{
			ID:      record.ID,
			Type:    record.Type,
			Name:    record.Name,
			Content: newIP,
			TTL:     120,
		}); err != nil {
			return errors.Wrap(err, "could not update the DNS record")
		}

		// Log the update.
		logrus.WithFields(logrus.Fields{
			"name":    record.Name,
			"content": record.Content,
		}).Info("updated record")
	}

	return nil
}

func GetZoneId(api *cloudflare.API, domainName string) (string, error) {
	// Split the domain name by periods.
	splitDomainName := strings.Split(domainName, ".")

	// The domain name must be at least 2 elements, a name and a tld.
	if len(splitDomainName) < 2 {
		return "", errors.Errorf("%s did not contain a TLD", domainName)
	}

	// Extract the zone name from the domain name. This should be the last two
	// period delimitered strings.
	zoneName := strings.Join(splitDomainName[len(splitDomainName)-2:], ".")

	// Fetch the zone ID
	zoneID, err := api.ZoneIDByName(zoneName) // Assuming example.com exists in your Cloudflare account already
	if err != nil {
		return "", errors.Wrap(err, "could not find zone by name")
	}

	logrus.WithField("zoneID", zoneID).Info("got zone id")
	return zoneID, nil
}
