package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"math/rand"
	"net"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/davecgh/go-spew/spew"
	"github.com/google/uuid"
	"github.com/jasonlvhit/gocron"
	"layeh.com/radius"
	"layeh.com/radius/rfc2865"
	"layeh.com/radius/rfc2866"
)

const usage = `
Sends an Accounting RADIUS packet to a server and prints the result.
`

type Schedule func(*client)

type client struct {
	UserName                 string
	NASIPAddress             string
	NASPort                  string
	FramedIPAddress          string
	CalledStationId          string
	CallingStationId         string
	NASIdentifier            string
	NASPortType              string
	AcctStatusType           rfc2866.AcctStatusType
	AcctDelayTime            string
	AcctInputOctets          int
	AcctOutputOctets         int
	AcctSessionId            string
	AcctAuthentic            string
	AcctInputPackets         int
	AcctOutputPackets        int
	AcctInputGigawords       int
	AcctOutputGigawords      int
	AcctSessionTime          int
	EventTimestamp           time.Time
	NASPortId                string
	Start                    Schedule
	ForceStop                bool
	PreviousAcctInputOctets  int
	PreviousAcctOutputOctets int
	PreviousAcctSessionTime  int
	PreviousAcctSessionId    string
}

type clients struct {
	client []*client
}

type config struct {
	host              *string
	port              *string
	secret            *string
	nbclient          *int
	interim           *int
	nasport           *string
	timeout           *time.Duration
	randomize         *bool
	stopthreshold     *int
	uniquemac         *bool
	spread            *bool
	start_before_stop *bool
}

func main() {

	logger := GetInstance()
	logger.Println("Starting")

	configuration := &config{}

	configuration.host = flag.String("host", "127.0.0.1", "Server ip")
	configuration.port = flag.String("port", "1813", "server port")
	configuration.secret = flag.String("secret", "testing123", "Shared secret")
	configuration.nbclient = flag.Int("number", 500, "Number of Calling-Station-Id")
	configuration.interim = flag.Int("interim", 360, "Number of second for the interim-update")
	configuration.nasport = flag.String("nasport", "1500", "Nas Port")
	configuration.timeout = flag.Duration("timeout", time.Second*10, "timeout for the request to finish")
	configuration.randomize = flag.Bool("random", true, "Randomize the accounting traffic")
	configuration.stopthreshold = flag.Int("threshold", 2, "Pourcent to send accounting stop on session")
	configuration.uniquemac = flag.Bool("unique", false, "Use the same mac address for the test")
	configuration.spread = flag.Bool("spread", true, "spread packet across interim")
	configuration.start_before_stop = flag.Bool("startstop", false, "Send start before stop")
	flag.Parse()

	if *configuration.stopthreshold > 100 {
		fmt.Println("\r- threshold can??t be greater to 100")
		os.Exit(0)
	}

	logger.Println(strings.Join(flag.CommandLine.Args(), " "))

	clientsMac := &clients{}
	for j := 1; j <= *configuration.nbclient; j++ {
		clientMac := &client{}
		if *configuration.uniquemac {
			clientMac.CallingStationId = "00:11:22:33:44:55"
		} else {
			clientMac.CallingStationId = GenerateMac().String()
		}
		clientMac.CalledStationId = GenerateMac().String() + ":" + RandStringRunes(5)
		clientMac.UserName = clientMac.CallingStationId
		clientMac.NASPortId = *configuration.nasport
		clientMac.AcctInputOctets = 0
		clientMac.AcctOutputOctets = 0
		clientMac.AcctStatusType = rfc2866.AcctStatusType_Value_Start
		clientMac.AcctSessionId = uuid.New().String()
		clientMac.ForceStop = false
		clientMac.Start = func(clientMac *client) {
			go func(clientMac *client) {
				s := gocron.NewScheduler()
				task(configuration, clientMac)
				s.Every(uint64(*configuration.interim)).Seconds().Do(task, configuration, clientMac)
				<-s.Start()
			}(clientMac)
		}
		clientsMac.client = append(clientsMac.client, clientMac)

	}
	// Setup our Ctrl+C handler
	SetupCloseHandler(configuration, clientsMac)

	go func() {
		rand.Seed(time.Now().UnixNano())
		for _, v := range clientsMac.client {
			v.Start(v)
			if *configuration.spread {
				time.Sleep(time.Duration(( *configuration.interim * 1000 ) / *configuration.nbclient) * time.Millisecond)
			} else {
				n := rand.Intn(*configuration.interim)
				time.Sleep(time.Duration(n) * time.Millisecond)
			}
		}
	}()

	s := gocron.NewScheduler()
	s.Every(1).Minutes().Do(hello)
	<-s.Start()

}

func hello() {
	spew.Dump("Hellow world")
}

func task(configuration *config, clientMac *client) {
	logger := GetInstance()
	logger.Println(clientMac.AcctStatusType.String() + " " + clientMac.CallingStationId)

	hostport := net.JoinHostPort(*configuration.host, *configuration.port)

	packet := radius.New(radius.CodeAccountingRequest, []byte(*configuration.secret))
	rfc2865.UserName_SetString(packet, clientMac.UserName)
	rfc2865.CallingStationID_Set(packet, []byte(clientMac.CallingStationId))
	rfc2865.CalledStationID_Add(packet, []byte(clientMac.CalledStationId))
	rfc2866.AcctStatusType_Add(packet, clientMac.AcctStatusType)
	rfc2866.AcctInputOctets_Add(packet, rfc2866.AcctInputOctets(clientMac.AcctInputOctets))
	rfc2866.AcctOutputOctets_Add(packet, rfc2866.AcctOutputOctets(clientMac.AcctOutputOctets))
	rfc2866.AcctSessionTime_Add(packet, rfc2866.AcctSessionTime(clientMac.AcctSessionTime))
	rfc2866.AcctSessionID_AddString(packet, clientMac.AcctSessionId)
	nasPort, _ := strconv.Atoi(clientMac.NASPortId)
	rfc2865.NASPort_Set(packet, rfc2865.NASPort(nasPort))

	ctx, cancel := context.WithTimeout(context.Background(), *configuration.timeout)
	defer cancel()
	received, err := radius.Exchange(ctx, packet, hostport)
	if err != nil {
		fmt.Println(err)
		return
	}

	status := received.Code.String()
	if msg, err := rfc2865.ReplyMessage_LookupString(received); err == nil {
		status += " (" + msg + ")"
	}

	fmt.Println(clientMac.CallingStationId + " : " + clientMac.AcctStatusType.String() + " : " + status)
	rand.Seed(time.Now().UnixNano())

	// We already send the start & stop. Send a Interim
	if clientMac.ForceStop && clientMac.AcctStatusType == rfc2866.AcctStatusType_Value_Stop {
		clientMac.AcctStatusType = rfc2866.AcctStatusType_Value_InterimUpdate
		o := rand.Intn(*configuration.interim * 100)
		i := rand.Intn(*configuration.interim * 100)
		clientMac.AcctInputOctets = clientMac.AcctInputOctets + i
		clientMac.AcctOutputOctets = clientMac.AcctOutputOctets + o
		clientMac.AcctSessionTime = clientMac.AcctSessionTime + *configuration.interim
		clientMac.AcctSessionId = clientMac.PreviousAcctSessionId
		clientMac.ForceStop = false
	}

	// Force accounting stop
	if clientMac.ForceStop {
		clientMac.AcctStatusType = rfc2866.AcctStatusType_Value_Stop
		o := rand.Intn(*configuration.interim * 100)
		i := rand.Intn(*configuration.interim * 100)
		clientMac.AcctInputOctets = clientMac.PreviousAcctInputOctets + i
		clientMac.AcctOutputOctets = clientMac.PreviousAcctOutputOctets + o
		clientMac.AcctSessionTime = clientMac.PreviousAcctSessionTime + *configuration.interim
		// Save the start session id
		previousAcctSessionId := clientMac.AcctSessionId
		clientMac.AcctSessionId = clientMac.PreviousAcctSessionId
		clientMac.PreviousAcctSessionId = previousAcctSessionId
		clientMac.PreviousAcctInputOctets = 0
		clientMac.PreviousAcctOutputOctets = 0
		clientMac.PreviousAcctSessionTime = 0
		return
	}

	if clientMac.AcctStatusType == rfc2866.AcctStatusType_Value_Start || clientMac.AcctStatusType == rfc2866.AcctStatusType_Value_InterimUpdate {
		n := rand.Intn(100)
		if *configuration.randomize {
			if n < 100-*configuration.stopthreshold {
				clientMac.AcctStatusType = rfc2866.AcctStatusType_Value_InterimUpdate
				o := rand.Intn(*configuration.interim * 100)
				i := rand.Intn(*configuration.interim * 100)
				clientMac.AcctInputOctets = clientMac.AcctInputOctets + i
				clientMac.AcctOutputOctets = clientMac.AcctOutputOctets + o
				clientMac.AcctSessionTime = clientMac.AcctSessionTime + *configuration.interim
			} else {
				if *configuration.start_before_stop {
					clientMac.AcctStatusType = rfc2866.AcctStatusType_Value_Start
					clientMac.PreviousAcctInputOctets = clientMac.AcctInputOctets
					clientMac.PreviousAcctOutputOctets = clientMac.AcctOutputOctets
					clientMac.PreviousAcctSessionTime = clientMac.AcctSessionTime
					clientMac.PreviousAcctSessionId = clientMac.AcctSessionId
					clientMac.AcctSessionId = uuid.New().String() + ":" + RandStringRunes(5)
					clientMac.AcctInputOctets = 0
					clientMac.AcctOutputOctets = 0
					clientMac.AcctSessionTime = 0
					clientMac.ForceStop = true
					// Wait a bit before sending the start
					n := rand.Intn(*configuration.interim)
					time.Sleep(time.Duration(n) * time.Second)
					// Send the start
					task(configuration, clientMac)
					return
				}
				clientMac.AcctStatusType = rfc2866.AcctStatusType_Value_Stop
				o := rand.Intn(*configuration.interim * 100)
				i := rand.Intn(*configuration.interim * 100)
				clientMac.AcctInputOctets = clientMac.AcctInputOctets + i
				clientMac.AcctOutputOctets = clientMac.AcctOutputOctets + o
				clientMac.AcctSessionTime = clientMac.AcctSessionTime + *configuration.interim
			}
		} else {
			clientMac.AcctStatusType = rfc2866.AcctStatusType_Value_InterimUpdate
			o := rand.Intn(*configuration.interim * 100)
			i := rand.Intn(*configuration.interim * 100)
			clientMac.AcctInputOctets = clientMac.AcctInputOctets + i
			clientMac.AcctOutputOctets = clientMac.AcctOutputOctets + o
			clientMac.AcctSessionTime = clientMac.AcctSessionTime + *configuration.interim
		}
	} else {
		clientMac.AcctStatusType = rfc2866.AcctStatusType_Value_Start
		clientMac.AcctInputOctets = 0
		clientMac.AcctOutputOctets = 0
		clientMac.AcctSessionTime = 0
		clientMac.AcctSessionId = uuid.New().String() + ":" + RandStringRunes(5)
	}
}

func GenerateMac() net.HardwareAddr {
	buf := make([]byte, 6)
	var mac net.HardwareAddr

	_, err := rand.Read(buf)
	if err != nil {
	}

	// Set the local bit
	buf[0] |= 2

	mac = append(mac, buf[0], buf[1], buf[2], buf[3], buf[4], buf[5])

	return mac
}

// SetupCloseHandler creates a 'listener' on a new goroutine which will notify the
// program if it receives an interrupt from the OS. We then handle this by calling
// our clean up procedure and exiting the program.
func SetupCloseHandler(configuration *config, clientsMac *clients) {
	c := make(chan os.Signal)
	signal.Notify(c, os.Interrupt, syscall.SIGTERM)
	go func(configuration *config, clientMac *clients) {
		<-c
		file, _ := os.OpenFile("logs.txt", os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0666)
		defer file.Close()
		for _, v := range clientsMac.client {
			v.AcctStatusType = rfc2866.AcctStatusType_Value_Stop
			task(configuration, v)
			fmt.Println("\r Stop for " + v.CallingStationId)
			log.SetOutput(file)
			log.Println("Stop for " + v.CallingStationId)
		}
		fmt.Println("\r- Ctrl+C pressed in Terminal")
		os.Exit(0)
	}(configuration, clientsMac)
}

var letterRunes = []rune("abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ")

func RandStringRunes(n int) string {
	rand.Seed(time.Now().UnixNano())
	b := make([]rune, n)
	for i := range b {
		b[i] = letterRunes[rand.Intn(len(letterRunes))]
	}
	return string(b)
}

