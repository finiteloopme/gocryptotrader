package sharedtestvalues

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/thrasher-corp/gocryptotrader/currency"
	exchange "github.com/thrasher-corp/gocryptotrader/exchanges"
	"github.com/thrasher-corp/gocryptotrader/exchanges/asset"
	"github.com/thrasher-corp/gocryptotrader/exchanges/stream"
	"github.com/thrasher-corp/gocryptotrader/exchanges/subscription"
)

// This package is only to be referenced in test files
const (
	// WebsocketResponseDefaultTimeout used in websocket testing
	// Defines wait time for receiving websocket response before cancelling
	WebsocketResponseDefaultTimeout = (3 * time.Second)
	// WebsocketResponseExtendedTimeout used in websocket testing
	// Defines wait time for receiving websocket response before cancelling
	WebsocketResponseExtendedTimeout = (15 * time.Second)
	// WebsocketChannelOverrideCapacity used in websocket testing
	// Defines channel capacity as defaults size can block tests
	WebsocketChannelOverrideCapacity = 500

	MockTesting = "Mock testing framework in use for %s exchange on REST endpoints only"
	LiveTesting = "Mock testing bypassed; live testing of REST endpoints in use for %s exchange"

	warningSkip             = "Skipping test"
	warningKeys             = "API test keys have not been set"
	warningManipulateOrders = "variable `canManipulateRealOrders` is false"
	warningHowTo            = "these values can be set at the top of the test file."
)

// GetWebsocketInterfaceChannelOverride returns a new interface based channel
// with the capacity set to WebsocketChannelOverrideCapacity
func GetWebsocketInterfaceChannelOverride() chan interface{} {
	return make(chan interface{}, WebsocketChannelOverrideCapacity)
}

// GetWebsocketStructChannelOverride returns a new struct based channel
// with the capacity set to WebsocketChannelOverrideCapacity
func GetWebsocketStructChannelOverride() chan struct{} {
	return make(chan struct{}, WebsocketChannelOverrideCapacity)
}

// NewTestWebsocket returns a test websocket object
func NewTestWebsocket() *stream.Websocket {
	return &stream.Websocket{
		Init:              true,
		DataHandler:       make(chan interface{}, WebsocketChannelOverrideCapacity),
		ToRoutine:         make(chan interface{}, 1000),
		TrafficAlert:      make(chan struct{}),
		ReadMessageErrors: make(chan error),
		Subscribe:         make(chan []subscription.Subscription, 10),
		Unsubscribe:       make(chan []subscription.Subscription, 10),
		Match:             stream.NewMatch(),
	}
}

// SkipTestIfCredentialsUnset is a test helper function checking if the
// authenticated function can perform the required test.
func SkipTestIfCredentialsUnset(t *testing.T, exch exchange.IBotExchange, canManipulateOrders ...bool) {
	t.Helper()

	if len(canManipulateOrders) > 1 {
		t.Fatal("more than one canManipulateOrders boolean value has been supplied, please remove")
	}

	areTestAPICredentialsSet := AreAPICredentialsSet(exch)
	supportsManipulatingOrders := len(canManipulateOrders) > 0
	allowedToManipulateOrders := supportsManipulatingOrders && canManipulateOrders[0]

	if (areTestAPICredentialsSet && !supportsManipulatingOrders) ||
		(areTestAPICredentialsSet && allowedToManipulateOrders) {
		return
	}

	message := []string{warningSkip}
	if !areTestAPICredentialsSet {
		message = append(message, warningKeys)
	}

	if supportsManipulatingOrders && !allowedToManipulateOrders {
		message = append(message, warningManipulateOrders)
	}
	message = append(message, warningHowTo)
	t.Skip(strings.Join(message, ", "))
}

// SkipTestIfCannotManipulateOrders will only skip if the credentials are set
// correctly and can manipulate orders is set to false. It will continue normal
// operations if credentials are not set, giving better code coverage.
func SkipTestIfCannotManipulateOrders(t *testing.T, exch exchange.IBotExchange, canManipulateOrders bool) {
	t.Helper()

	if !AreAPICredentialsSet(exch) || canManipulateOrders {
		return
	}

	t.Skip(warningSkip + ", " + warningManipulateOrders)
}

// AreAPICredentialsSet returns if the API credentials are set.
func AreAPICredentialsSet(exch exchange.IBotExchange) bool {
	return exch.VerifyAPICredentials(exch.GetDefaultCredentials()) == nil
}

// EmptyStringPotentialPattern is a regular expression pattern for a potential
// empty string into float64
var EmptyStringPotentialPattern = `.*float64.*json:"[^"]*,string".*`

// ForceFileStandard will check all files in the current directory for a regular
// expression pattern. If the pattern is found the test will fail.
func ForceFileStandard(t *testing.T, pattern string) error {
	t.Helper()

	r := regexp.MustCompile(pattern)

	root := "." // Specify the root directory to start walking from
	err := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}

		if !info.IsDir() && strings.HasSuffix(path, ".go") {
			fileContents, err := os.ReadFile(path)
			if err != nil {
				t.Fatalf("Failed to read file: %v", err)
			}

			lines := bytes.Split(fileContents, []byte("\n"))
			for x, line := range lines {
				if r.Match(line) {
					t.Errorf("File: %s line contains pattern [%s] match with [%s] at line %d", path, pattern, string(line), x+1)
				}
			}
		}
		return nil
	})

	if err != nil {
		return fmt.Errorf("failed to walk directory: %w", err)
	}
	return nil
}

// TestFixtureToDataHandler takes a new empty exchange and configures a new websocket handler for it, and squirts the json path contents to it
// It accepts a reader function, which is probably e.wsHandleData but could be anything
func TestFixtureToDataHandler(t *testing.T, seed, e exchange.IBotExchange, fixturePath string, reader func([]byte) error) {
	t.Helper()
	b := e.GetBase()
	seedBase := seed.GetBase()

	err := b.CurrencyPairs.Load(&seedBase.CurrencyPairs)
	assert.NoError(t, err, "Loading currency pairs should not error")

	b.Name = "fixture"
	b.Websocket = &stream.Websocket{
		Wg:          new(sync.WaitGroup),
		DataHandler: make(chan interface{}, 128),
	}
	b.API.Endpoints = b.NewEndpoints()

	fixture, err := os.Open(fixturePath)
	assert.NoError(t, err, "Opening fixture '%s' should not error", fixturePath)
	defer func() {
		assert.NoError(t, fixture.Close(), "Closing the fixture file should not error")
	}()

	s := bufio.NewScanner(fixture)
	for s.Scan() {
		msg := s.Bytes()
		err := reader(msg)
		assert.NoErrorf(t, err, "Fixture message should not error:\n%s", msg)
	}
	assert.NoError(t, s.Err(), "Fixture Scanner should not error")
}

// SetupCurrencyPairsForExchangeAsset enables an asset for an exchange
// and adds the currency pair(s) to the available and enabled list of existing pairs
// if it is already enabled or part of the pairs, no error is raised
func SetupCurrencyPairsForExchangeAsset(t *testing.T, exch exchange.IBotExchange, a asset.Item, cp ...currency.Pair) {
	t.Helper()
	if len(cp) == 0 {
		return
	}
	b := exch.GetBase()
	err := b.CurrencyPairs.SetAssetEnabled(a, true)
	if err != nil && !errors.Is(err, currency.ErrAssetAlreadyEnabled) {
		t.Fatal(err)
	}
	availPairs, err := b.CurrencyPairs.GetPairs(a, false)
	if err != nil {
		t.Fatal(err)
	}
	apLen := len(availPairs)
	enabledPairs, err := b.CurrencyPairs.GetPairs(a, true)
	if err != nil {
		t.Fatal(err)
	}
	epLen := len(enabledPairs)
	for i := range cp {
		availPairs = availPairs.Add(cp[i])
		enabledPairs = enabledPairs.Add(cp[i])
	}
	if len(availPairs) != apLen {
		err = b.CurrencyPairs.StorePairs(a, availPairs, false)
		if err != nil {
			t.Fatal(err)
		}
	}
	if len(enabledPairs) != epLen {
		err = b.CurrencyPairs.StorePairs(a, enabledPairs, true)
		if err != nil {
			t.Fatal(err)
		}
	}
}
