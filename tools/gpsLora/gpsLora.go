package main

import (
	"bufio"
	"encoding/hex"
	"log"
	"strconv"
	"strings"
	"time"

	"github.com/pkg/errors"

	"github.com/adrianmo/go-nmea"
	"github.com/calvernaz/rak811"
	"github.com/tarm/serial"
)

func main() {
	log.SetFlags(log.Ltime | log.Lshortfile)
	app := kingpin.New(filepath.Base(os.Args[0]), "A tool that connects to the Adafruit USB GPS board and sends the data to a RAK811 module")
	app.HelpFlag.Short('h')

	nwksKey := app.Flag("nwks_key", "lora server nwks_key").
		Required().
		String()
	devEUI := app.Flag("dev_eui", "lora server dev_eui").
		Required().
		String()
	appKey := app.Flag("app_key", "lora server app_key").
		Required().
		String()

	if _, err := app.Parse(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, errors.Wrapf(err, "Error parsing commandline arguments"))
		app.Usage(os.Args[1:])
		os.Exit(2)
	}
	respChan, err := enableGPS()
	if err != nil {
		log.Fatal("failed to enable gps err:", err)
	}

	lora, err := newLoraConnection(nwksKey, devEUI, appKey)
	if err != nil {
		log.Fatal("failed to create lora connection err:", err)
	}

	for {
		dataGPS := <-respChan
		dataLora := hex.EncodeToString([]byte(strconv.FormatFloat(dataGPS.Latitude, 'f', -1, 64) + "," + strconv.FormatFloat(dataGPS.Longitude, 'f', -1, 64)))

		log.Println("sending data", dataGPS, "len", len(dataLora))
		_, err := lora.Send("0,1," + dataLora)
		if err != nil {
			log.Println("failed to send data err:", err)
		}
	}
}

func enableGPS() (chan nmea.RMC, error) {
	c := &serial.Config{Name: "/dev/ttyUSB0", Baud: 9600, ReadTimeout: 3000 * time.Second}
	s, err := serial.OpenPort(c)
	if err != nil {
		return nil, errors.Wrap(err, "enable port")
	}

	reader := bufio.NewReader(s)

	// Full ref: https://cdn-shop.adafruit.com/datasheets/PMTK_A08.pdf
	// Turn on just minimum info (RMC only, location):
	command := "PMTK314,0,1,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0"
	s.Write([]byte("$" + command + "*" + nmea.XORChecksum(command) + "\r\n"))

	if !gerMTKAck(314, reader) {
		return nil, errors.New("no cmd ack")
	}

	// Set update rate to once every 10 second (10hz).
	command = "PMTK220,10000"
	s.Write([]byte("$" + command + "*" + nmea.XORChecksum(command) + "\r\n"))
	if !gerMTKAck(220, reader) {
		return nil, errors.New("no cmd ack")
	}

	respChan := make(chan nmea.RMC)
	go func() {
		for {
			line, err := reader.ReadString('\n')
			if err != nil {
				log.Fatalf("reading gps serial err:%v", err)
			}
			line = strings.TrimSpace(line)
			parsed, err := nmea.Parse(line)
			if err != nil {
				log.Println("unable to parse GPS response err:", err)
				continue
			}
			if parsed.DataType() == nmea.TypeRMC {
				dataGPS := parsed.(nmea.RMC)
				// Send only GPS data if it is valid.
				if dataGPS.Validity != nmea.ValidRMC {
					log.Println("skip sending invalid GPS data", dataGPS)
					continue
				}
				respChan <- dataGPS
			}
		}
	}()

	return respChan, nil
}

func newLoraConnection(nwksKey, devEUI, appKey string) (*rak811.Lora, error) {
	cfg := &serial.Config{
		Name:        "/dev/ttyAMA0",
		ReadTimeout: 25000 * time.Millisecond,
	}
	lora, err := rak811.New(cfg)
	if err != nil {
		return nil, errors.Wrap(err, "create rak811 instance")
	}
	log.Println("lora module initialized")

	resp, err := lora.HardReset()
	if err != nil {
		return nil, errors.Wrap(err, "reset module")
	}
	log.Println("lora module reset resp:", resp)

	resp, err = lora.SetMode(0)
	if err != nil {
		return nil, errors.Wrapf(err, "set lora mod")
	}
	log.Println("lora module mode set resp:", resp)

	resp, err = lora.SetConfig("nwks_key:" + nwksKey + "&dev_eui:" + devEUI + "&app_key:" + appKey + "&app_eui:0000010000000000")
	if err != nil {
		return nil, errors.Wrapf(err, "set lora config")
	}
	log.Println("lora module config set resp:", resp)

	resp, err = lora.JoinOTAA()
	if err != nil {
		return nil, errors.Wrapf(err, "lora join")
	}
	log.Println("lora module joined resp:", resp)

	return lora, nil
}

// gerCmdAck reads untill it gets an ack for the initial setup command or
// untill it reached a reader error.
// This ic because the module might be currenlty active so
// might receive another response before the ack recponse.
func gerMTKAck(cmdID int, reader *bufio.Reader) bool {
	var ok bool
	for x := 0; x < 20; x++ {
		line, err := reader.ReadString('\n')
		if err != nil {
			break
		}
		resp, err := nmea.Parse(strings.TrimSpace(line))
		if resp.TalkerID() == nmea.TypeMTK {
			d := resp.(nmea.MTK)
			if d.Cmd == cmdID && d.Flag == 3 {
				ok = true
				break
			}
		}
	}
	return ok
}
