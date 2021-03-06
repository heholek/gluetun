package main

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/qdm12/gluetun/internal/alpine"
	"github.com/qdm12/gluetun/internal/cli"
	"github.com/qdm12/gluetun/internal/constants"
	"github.com/qdm12/gluetun/internal/dns"
	"github.com/qdm12/gluetun/internal/firewall"
	gluetunLogging "github.com/qdm12/gluetun/internal/logging"
	"github.com/qdm12/gluetun/internal/openvpn"
	"github.com/qdm12/gluetun/internal/params"
	"github.com/qdm12/gluetun/internal/publicip"
	"github.com/qdm12/gluetun/internal/routing"
	"github.com/qdm12/gluetun/internal/server"
	"github.com/qdm12/gluetun/internal/settings"
	"github.com/qdm12/gluetun/internal/shadowsocks"
	"github.com/qdm12/gluetun/internal/storage"
	"github.com/qdm12/gluetun/internal/tinyproxy"
	"github.com/qdm12/gluetun/internal/updater"
	versionpkg "github.com/qdm12/gluetun/internal/version"
	"github.com/qdm12/golibs/command"
	"github.com/qdm12/golibs/files"
	"github.com/qdm12/golibs/logging"
	"github.com/qdm12/golibs/network"
)

//nolint:gochecknoglobals
var (
	version   = "unknown"
	commit    = "unknown"
	buildDate = "an unknown date"
)

func main() {
	ctx := context.Background()
	os.Exit(_main(ctx, os.Args))
}

func _main(background context.Context, args []string) int { //nolint:gocognit,gocyclo
	if len(args) > 1 { // cli operation
		var err error
		switch args[1] {
		case "healthcheck":
			err = cli.HealthCheck()
		case "clientkey":
			err = cli.ClientKey(args[2:])
		case "openvpnconfig":
			err = cli.OpenvpnConfig()
		case "update":
			err = cli.Update(args[2:])
		default:
			err = fmt.Errorf("command %q is unknown", args[1])
		}
		if err != nil {
			fmt.Println(err)
			return 1
		}
		return 0
	}
	ctx, cancel := context.WithCancel(background)
	defer cancel()
	logger := createLogger()

	httpClient := &http.Client{Timeout: 15 * time.Second}
	client := network.NewClient(15 * time.Second)
	// Create configurators
	fileManager := files.NewFileManager()
	alpineConf := alpine.NewConfigurator(fileManager)
	ovpnConf := openvpn.NewConfigurator(logger, fileManager)
	dnsConf := dns.NewConfigurator(logger, client, fileManager)
	routingConf := routing.NewRouting(logger, fileManager)
	firewallConf := firewall.NewConfigurator(logger, routingConf, fileManager)
	tinyProxyConf := tinyproxy.NewConfigurator(fileManager, logger)
	streamMerger := command.NewStreamMerger()

	paramsReader := params.NewReader(logger, fileManager)
	fmt.Println(gluetunLogging.Splash(version, commit, buildDate))

	printVersions(ctx, logger, map[string]func(ctx context.Context) (string, error){
		"OpenVPN":   ovpnConf.Version,
		"Unbound":   dnsConf.Version,
		"IPtables":  firewallConf.Version,
		"TinyProxy": tinyProxyConf.Version,
	})

	allSettings, err := settings.GetAllSettings(paramsReader)
	if err != nil {
		logger.Error(err)
		return 1
	}
	logger.Info(allSettings.String())

	// TODO run this in a loop or in openvpn to reload from file without restarting
	storage := storage.New(logger)
	const updateServerFile = true
	allServers, err := storage.SyncServers(constants.GetAllServers(), updateServerFile)
	if err != nil {
		logger.Error(err)
		return 1
	}

	// Should never change
	uid, gid := allSettings.System.UID, allSettings.System.GID

	err = alpineConf.CreateUser("nonrootuser", uid)
	if err != nil {
		logger.Error(err)
		return 1
	}
	err = fileManager.SetOwnership("/etc/unbound", uid, gid)
	if err != nil {
		logger.Error(err)
		return 1
	}
	err = fileManager.SetOwnership("/etc/tinyproxy", uid, gid)
	if err != nil {
		logger.Error(err)
		return 1
	}

	if allSettings.Firewall.Debug {
		firewallConf.SetDebug()
		routingConf.SetDebug()
	}

	defaultInterface, defaultGateway, err := routingConf.DefaultRoute()
	if err != nil {
		logger.Error(err)
		return 1
	}

	localSubnet, err := routingConf.LocalSubnet()
	if err != nil {
		logger.Error(err)
		return 1
	}

	firewallConf.SetNetworkInformation(defaultInterface, defaultGateway, localSubnet)

	if err := ovpnConf.CheckTUN(); err != nil {
		logger.Warn(err)
		err = ovpnConf.CreateTUN()
		if err != nil {
			logger.Error(err)
			return 1
		}
	}

	tunnelReadyCh, dnsReadyCh := make(chan struct{}), make(chan struct{})
	signalTunnelReady := func() { tunnelReadyCh <- struct{}{} }
	signalDNSReady := func() { dnsReadyCh <- struct{}{} }
	defer close(tunnelReadyCh)
	defer close(dnsReadyCh)

	if allSettings.Firewall.Enabled {
		err := firewallConf.SetEnabled(ctx, true) // disabled by default
		if err != nil {
			logger.Error(err)
			return 1
		}
	}

	err = firewallConf.SetAllowedSubnets(ctx, allSettings.Firewall.AllowedSubnets)
	if err != nil {
		logger.Error(err)
		return 1
	}

	for _, vpnPort := range allSettings.Firewall.VPNInputPorts {
		err = firewallConf.SetAllowedPort(ctx, vpnPort, string(constants.TUN))
		if err != nil {
			logger.Error(err)
			return 1
		}
	}

	wg := &sync.WaitGroup{}

	go collectStreamLines(ctx, streamMerger, logger, signalTunnelReady)

	openvpnLooper := openvpn.NewLooper(allSettings.VPNSP, allSettings.OpenVPN, uid, gid, allServers,
		ovpnConf, firewallConf, routingConf, logger, httpClient, fileManager, streamMerger, cancel)
	wg.Add(1)
	// wait for restartOpenvpn
	go openvpnLooper.Run(ctx, wg)

	updaterOptions := updater.NewOptions("127.0.0.1")
	updaterLooper := updater.NewLooper(updaterOptions, allSettings.UpdaterPeriod, allServers, storage, openvpnLooper.SetAllServers, httpClient, logger)
	wg.Add(1)
	// wait for updaterLooper.Restart() or its ticket launched with RunRestartTicker
	go updaterLooper.Run(ctx, wg)

	unboundLooper := dns.NewLooper(dnsConf, allSettings.DNS, logger, streamMerger, uid, gid)
	wg.Add(1)
	// wait for unboundLooper.Restart or its ticker launched with RunRestartTicker
	go unboundLooper.Run(ctx, wg, signalDNSReady)

	publicIPLooper := publicip.NewLooper(client, logger, fileManager, allSettings.System.IPStatusFilepath, allSettings.PublicIPPeriod, uid, gid)
	wg.Add(1)
	go publicIPLooper.Run(ctx, wg)
	wg.Add(1)
	go publicIPLooper.RunRestartTicker(ctx, wg)
	publicIPLooper.SetPeriod(allSettings.PublicIPPeriod) // call after RunRestartTicker

	tinyproxyLooper := tinyproxy.NewLooper(tinyProxyConf, firewallConf, allSettings.TinyProxy, logger, streamMerger, uid, gid, defaultInterface)
	restartTinyproxy := tinyproxyLooper.Restart
	wg.Add(1)
	go tinyproxyLooper.Run(ctx, wg)

	shadowsocksLooper := shadowsocks.NewLooper(firewallConf, allSettings.ShadowSocks, logger, defaultInterface)
	restartShadowsocks := shadowsocksLooper.Restart
	wg.Add(1)
	go shadowsocksLooper.Run(ctx, wg)

	if allSettings.TinyProxy.Enabled {
		restartTinyproxy()
	}
	if allSettings.ShadowSocks.Enabled {
		restartShadowsocks()
	}

	wg.Add(1)
	go routeReadyEvents(ctx, wg, tunnelReadyCh, dnsReadyCh,
		unboundLooper, updaterLooper, publicIPLooper, routingConf, logger, httpClient,
		allSettings.VersionInformation, allSettings.OpenVPN.Provider.PortForwarding.Enabled, openvpnLooper.PortForward,
	)

	httpServer := server.New("0.0.0.0:8000", logger, openvpnLooper, unboundLooper, updaterLooper)
	wg.Add(1)
	go httpServer.Run(ctx, wg)

	// Start openvpn for the first time
	openvpnLooper.Restart()

	signalsCh := make(chan os.Signal, 1)
	signal.Notify(signalsCh,
		syscall.SIGINT,
		syscall.SIGTERM,
		os.Interrupt,
	)
	shutdownErrorsCount := 0
	select {
	case signal := <-signalsCh:
		logger.Warn("Caught OS signal %s, shutting down", signal)
		cancel()
	case <-ctx.Done():
		logger.Warn("context canceled, shutting down")
	}
	logger.Info("Clearing ip status file %s", allSettings.System.IPStatusFilepath)
	if err := fileManager.Remove(string(allSettings.System.IPStatusFilepath)); err != nil {
		logger.Error(err)
		shutdownErrorsCount++
	}
	if allSettings.OpenVPN.Provider.PortForwarding.Enabled {
		logger.Info("Clearing forwarded port status file %s", allSettings.OpenVPN.Provider.PortForwarding.Filepath)
		if err := fileManager.Remove(string(allSettings.OpenVPN.Provider.PortForwarding.Filepath)); err != nil {
			logger.Error(err)
			shutdownErrorsCount++
		}
	}
	const shutdownGracePeriod = 5 * time.Second
	waiting, waited := context.WithTimeout(context.Background(), shutdownGracePeriod)
	go func() {
		defer waited()
		wg.Wait()
	}()
	<-waiting.Done()
	if waiting.Err() == context.DeadlineExceeded {
		if shutdownErrorsCount > 0 {
			logger.Warn("Shutdown had %d errors", shutdownErrorsCount)
		}
		logger.Warn("Shutdown timed out")
		return 1
	}
	if shutdownErrorsCount > 0 {
		logger.Warn("Shutdown had %d errors")
		return 1
	}
	logger.Info("Shutdown successful")
	return 0
}

func createLogger() logging.Logger {
	logger, err := logging.NewLogger(logging.ConsoleEncoding, logging.InfoLevel, -1)
	if err != nil {
		panic(err)
	}
	return logger
}

func printVersions(ctx context.Context, logger logging.Logger, versionFunctions map[string]func(ctx context.Context) (string, error)) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	for name, f := range versionFunctions {
		version, err := f(ctx)
		if err != nil {
			logger.Error(err)
		} else {
			logger.Info("%s version: %s", name, version)
		}
	}
}

func collectStreamLines(ctx context.Context, streamMerger command.StreamMerger, logger logging.Logger, signalTunnelReady func()) {
	// Blocking line merging paramsReader for all programs: openvpn, tinyproxy, unbound and shadowsocks
	logger.Info("Launching standard output merger")
	streamMerger.CollectLines(ctx, func(line string) {
		line, level := gluetunLogging.PostProcessLine(line)
		if line == "" {
			return
		}
		switch level {
		case logging.InfoLevel:
			logger.Info(line)
		case logging.WarnLevel:
			logger.Warn(line)
		case logging.ErrorLevel:
			logger.Error(line)
		}
		switch {
		case strings.Contains(line, "Initialization Sequence Completed"):
			signalTunnelReady()
		case strings.Contains(line, "TLS Error: TLS key negotiation failed to occur within 60 seconds (check your network connectivity)"):
			logger.Warn("This means that either...")
			logger.Warn("1. The VPN server IP address you are trying to connect to is no longer valid, see https://github.com/qdm12/gluetun/wiki/Update-servers-information")
			logger.Warn("2. The VPN server crashed, try changing region")
			logger.Warn("3. Your Internet connection is not working, ensure it works")
			logger.Warn("Feel free to create an issue at https://github.com/qdm12/gluetun/issues/new/choose")
		}
	}, func(err error) {
		logger.Warn(err)
	})
}

//nolint:gocognit
func routeReadyEvents(ctx context.Context, wg *sync.WaitGroup, tunnelReadyCh, dnsReadyCh <-chan struct{},
	unboundLooper dns.Looper, updaterLooper updater.Looper, publicIPLooper publicip.Looper,
	routing routing.Routing, logger logging.Logger, httpClient *http.Client,
	versionInformation, portForwardingEnabled bool, startPortForward func(vpnGateway net.IP)) {
	defer wg.Done()
	tickerWg := &sync.WaitGroup{}
	// for linters only
	var restartTickerContext context.Context
	var restartTickerCancel context.CancelFunc = func() {}
	for {
		select {
		case <-ctx.Done():
			restartTickerCancel() // for linters only
			tickerWg.Wait()
			return
		case <-tunnelReadyCh: // blocks until openvpn is connected
			unboundLooper.Restart()
			restartTickerCancel() // stop previous restart tickers
			tickerWg.Wait()
			restartTickerContext, restartTickerCancel = context.WithCancel(ctx)
			tickerWg.Add(2)
			go unboundLooper.RunRestartTicker(restartTickerContext, tickerWg)
			go updaterLooper.RunRestartTicker(restartTickerContext, tickerWg)
			defaultInterface, _, err := routing.DefaultRoute()
			if err != nil {
				logger.Warn(err)
			} else {
				vpnDestination, err := routing.VPNDestinationIP(defaultInterface)
				if err != nil {
					logger.Warn(err)
				} else {
					logger.Info("VPN routing IP address: %s", vpnDestination)
				}
			}
			if portForwardingEnabled {
				// TODO make instantaneous once v3 go out of service
				const waitDuration = 5 * time.Second
				timer := time.NewTimer(waitDuration)
				select {
				case <-ctx.Done():
					if !timer.Stop() {
						<-timer.C
					}
					continue
				case <-timer.C:
					// vpnGateway required only for PIA v4
					vpnGateway, err := routing.VPNLocalGatewayIP()
					if err != nil {
						logger.Error(err)
					}
					logger.Info("VPN gateway IP address: %s", vpnGateway)
					startPortForward(vpnGateway)
				}
			}
		case <-dnsReadyCh:
			publicIPLooper.Restart() // TODO do not restart if disabled
			if !versionInformation {
				break
			}
			message, err := versionpkg.GetMessage(version, commit, httpClient)
			if err != nil {
				logger.Error(err)
				break
			}
			logger.Info(message)
		}
	}
}
