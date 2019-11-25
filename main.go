package main

import (
	"github.com/appf-anu/chamber-tools"
	"flag"
	"fmt"
	"github.com/bcampbell/fuzzytime"
	"github.com/jacobsa/go-serial/serial"
	"github.com/mdaffin/go-telegraf"
	"io"
	"log"
	"math"
	"math/rand"
	"os"
	"regexp"
	"strings"
	"time"
	"os/signal"
	"syscall"
)

const (
	adrtOne   = 0x01
	adrtGroup = 0x02
	adrtAll   = 0x0F
)

//for light channels
const (
	instrSetIntensity = 0
	instrGetIntensity = 1
	instrSetTrigger   = 2
	instrGetTrigger   = 3
)

//for service channel 0 = absolute 2 - gid and err
const (
	instrSetGid   = 0
	instrGetGid   = 1
	instrGetError = 2
	//for service channel 1 = absolute 9 - set speed
	instrSetSpeed = 3
	//for service channel 2 = absolute 15 - temp
	instrGetTemp = 3
)

const (
	serviceChannel0 = 2
	serviceChannel1 = 9 // all on
	serviceChannel2 = 15
)

const (
	channel1 = 0
	channel2 = 1
	channel3 = 3
	channel4 = 4
	channel5 = 5
	channel6 = 6
	channel7 = 7
	channel8 = 8
)

var availableChannels = []int{
	channel1,
	channel2,
	channel3,
	channel4,
	channel5,
	channel6,
	channel7,
	channel8,
	//serviceChannel1, // all on
}

const (
	errnoLed = 0x01
	errnoFet = 0x02
	errnoEe  = 0x04
	errnoExt = 0x10
	errnoDac = 0x20
	errnoCom = 0x80
)

const (
	opcodeGetLightColor    = 40
	opcodeGetVoltage       = 41
	opcodeGetResistance    = 42
	opcodeGetDacMap        = 43
	opcodeGetSwitchMap     = 44
	opcodeGetDeviceAddress = 61
	opcodeGetDeviceFamily  = 62
	opcodeGetChipTemp      = 63
)

const (
	//versions of answer
	//parse answer
	cmdWithAns = 0
	//get answer, but no need to parse
	cmdDummyAns = 1
	//no answer expected
	cmdNoAns = 2
)

var (
	errLog     *log.Logger
	ctx        fuzzytime.Context
	zoneName   string
	zoneOffset int
)

var (
	doTheDiscoMode                            bool
	doTheScrollMode                           bool
	allZero                                   bool
	noMetrics, dummy, absolute                bool
	conditionsPath, hostTag, groupTag, didTag string
	interval                                  time.Duration
	loopFirstDay                                  bool
)

var port io.ReadWriteCloser

const (
	matchFloatExp = `[-+]?\d*\.\d+|\d+`
	matchIntsExp  = `\b(\d+)\b`
)

// TsRegex is a regexp to find a timestamp within a filename
var /* const */ matchFloat = regexp.MustCompile(matchFloatExp)
var /* const */ matchInts = regexp.MustCompile(matchIntsExp)

// ConstructPacket constructs a packet according to the protocol laid out by PSI to set a channel to value.
// address is the "address" of the light, a non-negative integer.
// lightChannel is the channel to set, 0,1,3,4,5,6,7,8. 2 has special meaning and so is excluded. 8 is all.
// instr is the type of op to do. 0 = set intensity, 2 = set trigger, 3 = ???
// value is the value to set within the packet, valid values are 0-1022.
// addrType is the packet type, for us this is always 1, leaving it here for future compatibility (if we actually find out
// how to do it.
func ConstructPacket(addrType, address, lightChannel, instr, value int) (packet [7]byte, err error) {
	if addrType != adrtOne && addrType != adrtGroup && addrType != adrtAll {
		err = fmt.Errorf("not valid packet address type")
		return
	}

	if addrType == adrtOne && (address == 0 || address >= 1000) {
		err = fmt.Errorf("address not valid for address type")
		return
	}

	if addrType == adrtGroup {
		address += 1000
	}

	// header always ST
	packet = [7]byte{'S', 'T', 0, 0, 0, 0, 0}
	// target + nibble of address
	packet[2] = byte((addrType << 4) | ((address >> 8) & 0x0F))
	// the rest of the address
	packet[3] = byte(address & 0xFF)

	// opcode + payload
	// opcode = 4b, channel + 2b instruction part
	// is 'instr' meant to be instr? G.
	// either way this should now be working... G.
	packet[4] = byte(((lightChannel << 4) & 0xF0) | ((instr << 2) & 0x0C) | ((value >> 8) & 0x03))
	packet[5] = byte(value & 0xFF)

	// checksum to get last byte

	for i := 0; i < 6; i++ {
		packet[6] ^= packet[i]
	}
	return
}

// CheckPacketLength checks the packet length and returns -1 of the packet length is invalid
func CheckPacketLength(answer []byte) int {
	answerLength := len(answer)
	if answerLength < 5 {
		return -1
	}

	//return packet - header UV - most of the times correct even for multiple devices
	//might be some junk at the begining, try to find UV position
	var pos int
	for pos = 0; pos < answerLength-5; pos++ {
		if answer[pos] == 'U' && answer[pos+1] == 'V' {
			break
		}
	}
	//UV nenalezeno?
	if pos == answerLength-5 {
		return -1
	}
	return pos
}

//---------------------------------------------------------------------------
//bool TFytoCtrl::ParsePacket(uint8_t *answer, int32_t len, uint16_t *res, uint32_t address, int32_t iopcode)
func ParsePacket(answer []byte, iopCode byte) (rType, rAddress, ropCode, res byte, err error) {
	pos := CheckPacketLength(answer)
	if pos < 0 {
		err = fmt.Errorf("neg pos for packt")
		return
	}

	//CRC packetu
	crc := byte(0)
	for i := pos; i < pos+7; i++ {
		crc ^= answer[i]
	}

	//chyba v CRC
	if crc != 0 {
		err = fmt.Errorf("CRC no match")
		return
	}

	packet := answer[pos:]

	//parsovani adresy a prijemce
	rType = (packet[2] >> 4)
	rAddress = (((packet[2] & 0x0F) << 8) | packet[3])

	//parsovani kodu operace a payloadu
	ropCode = (packet[4] >> 2)
	res = ((packet[4] & 0x03) << 8) | packet[5]

	if rType == adrtOne && rAddress == 0 && ropCode == iopCode {
		return
	}
	err = fmt.Errorf("Everything is not alright: %s %s %s %s", rType, rAddress, ropCode, res)
	return
}

func ActivatePacket(lightChannel int) (packet []byte, err error) {
	// for some reason value needs to be a non-negative non-zero integer for these packets to correctly activate lights
	part1, err := ConstructPacket(adrtAll, 0, lightChannel, instrSetTrigger, 1) // set trigger on
	part2, err := ConstructPacket(adrtAll, 0, lightChannel, instrSetSpeed, 1)   // ???
	// spread em
	packet = append(part1[:], part2[:]...)
	return
}

func DeActivatePacket(lightChannel int) (packet []byte, err error) {

	part1, err := ConstructPacket(adrtAll, 0, lightChannel, instrSetTrigger, 0) // set trigger off
	part2, err := ConstructPacket(adrtAll, 0, lightChannel, instrSetSpeed, 0)   // ???
	// spread em
	packet = append(part1[:], part2[:]...)
	return
}

func SetIntensityPacket(lightChannel, value int) (packet []byte, err error) {
	pack, err := ConstructPacket(adrtAll, 0, lightChannel, instrSetIntensity, value) // set trigger on
	packet = pack[:]
	return
}

func random(max int) int {
	return rand.Intn(max)
}

func setOne(port io.ReadWriteCloser, lightChannel, value int) (err error) {
	if value < 0 {
		return nil
	}
	intensityPackt, err := SetIntensityPacket(lightChannel, value)
	if err != nil {
		errLog.Println(err)
		return
	}
	activatePackt, err := ActivatePacket(lightChannel)
	if err != nil {
		errLog.Println(err)
		return
	}

	port.Write(intensityPackt)
	port.Write(activatePackt)
	return nil
}

func setMany(port io.ReadWriteCloser, values []int) (err error) {
	for i := 0; i < len(values) && i < len(availableChannels); i++ {
		lightChannel := availableChannels[i]
		value := values[i]
		if value < 0 {
			continue // dont set values less than 0.
		}
		intensityPackt, err := SetIntensityPacket(lightChannel, value)

		if err != nil {
			errLog.Println(err)
			return err
		}

		activatePackt, err := ActivatePacket(lightChannel)
		if err != nil {
			errLog.Println(err)
			return err
		}

		port.Write(intensityPackt)
		port.Write(activatePackt)
	}

	return nil
}

func scrollMode(max int) {
	if max > 1022 {
		max = 1022
	}
	setAllZero()

	scrollOne := func(lightChannel int) {
		fmt.Println("Scrolling channel ", lightChannel)
		for value := -math.Pi / 2; value <= 1.5*math.Pi; value += 0.1 {
			sinV := (1 + math.Sin(value)) / 2
			thisV := sinV * float64(max)
			setOne(port, lightChannel, int(math.Round(thisV)))
			time.Sleep(100 * time.Millisecond)
		}
	}

	for {
		for _, lightChannel := range availableChannels {
			scrollOne(lightChannel)
		}
		scrollOne(serviceChannel1)
		time.Sleep(1 * time.Second)
	}
}

func setAllRandom(port io.ReadWriteCloser, max int) {
	for i := 1; i < len(availableChannels); i++ {
		lightChannel := availableChannels[i]
		value := random(max)
		intensityPackt, err := SetIntensityPacket(lightChannel, value)
		if err != nil {
			errLog.Println(err)
		}
		activatePackt, err := ActivatePacket(lightChannel)
		if err != nil {
			errLog.Println(err)
		}
		port.Write(intensityPackt)
		port.Write(activatePackt)
	}
}

func writeMetrics(lightValues []int) error {
	if !noMetrics {
		telegrafHost := "telegraf:8092"
		if os.Getenv("TELEGRAF_HOST") != "" {
			telegrafHost = os.Getenv("TELEGRAF_HOST")
		}

		telegrafClient, err := telegraf.NewUDP(telegrafHost)
		if err != nil {
			return err
		}
		defer telegrafClient.Close()

		m := telegraf.NewMeasurement("psi-light")
		for i, v := range lightValues {
			m.AddInt(fmt.Sprintf("chan-%d", i), v)
		}
		if hostTag != "" {
			m.AddTag("host", hostTag)
		}
		if groupTag != "" {
			m.AddTag("group", groupTag)
		}
		if didTag != "" {
			m.AddTag("user", didTag)
		}

		telegrafClient.Write(m)
	}
	return nil
}

// runStuff, should send values and write metrics.
// returns true if program should continue, false if program should retry
func runStuff(point *chamber_tools.TimePoint) bool {

	minLength := chamber_tools.Min(len(availableChannels), len(point.Channels))
	if len(point.Channels) < len(availableChannels){
		errLog.Printf("Number of light values in control file (%d) less than channels for this light (%d)," +
			" ignoring some channels.\n", len(point.Channels), len(availableChannels))
	}
	if len(point.Channels) > len(availableChannels) {
		errLog.Printf("Number of light values in control file (%d) greater than channels for this light (%d)," +
			" ignoring some channels.\n", len(point.Channels), len(availableChannels))
	}

	// setup multiplier
	multiplier := 1.0
	if !absolute{
		multiplier = 10.22
	}

	intVals := make([]int, minLength)
	for i, _ := range intVals {
		if point.Channels[i] < 0{
			intVals[i] = -1
		}
		// convert from percentage if we are not using absolute values.
		intVals[i] = chamber_tools.Clamp(int(point.Channels[i] * multiplier), -1, 1000)

	}


	if err := setMany(port, intVals); err != nil {
		errLog.Println(err)
		return false
	}

	// success
	errLog.Printf("ran %s %+v", point.Datetime.Format(time.RFC3339), intVals)
	for x := 0; x < 5; x++ {
		if err := writeMetrics(intVals); err != nil {
			errLog.Println(err)
			time.Sleep(200 * time.Millisecond)
			continue
		}
		break
	}

	return true
}

func setAllZero() {
	activatePackt, err := DeActivatePacket(serviceChannel1)
	if err != nil {
		errLog.Println(err)
	}
	port.Write(activatePackt)
}

func discoMode(max int) {
	ticker := time.NewTicker(500 * time.Millisecond)
	for range ticker.C {
		setAllRandom(port, max)
	}
}

var usage = func() {
	use := `
usage of %s:
flags:
	-no-metrics: dont collect or send metrics to telegraf
	-dummy: dont control the lights, only collect metrics
	-conditions: conditions to use to run the lights
	-interval: what interval to run conditions/record metrics at, set to 0s to read 1 metric and exit. (default=10m)

examples:
	collect data on 192.168.1.3  and output the errors to GC03-error.log and record the output to GC03.log
	%s -dummy 192.168.1.3 2>> GC03-error.log 1>> GC03.log

	run conditions on 192.168.1.3  and output the errors to GC03-error.log and record the output to GC03.log
	%s -conditions GC03-conditions.csv -dummy 192.168.1.3 2>> GC03-error.log 1>> GC03.log

quirks:
	the first 3 or 4 columns are used for running the chamber:
		date,time,temperature,humidity OR datetime,temperature,humidity
		the second case only occurs if the first 8 characters of the file (0th header) is "datetime"

	for the moment, the first line of the csv is technically (this is for your headers)
	if both -dummy and -no-metrics are specified, this program will exit.

`

	fmt.Printf(use, os.Args[0])
}

func init() {
	var err error
	errLog = log.New(os.Stderr, "[fytopanel] ", log.Ldate|log.Ltime|log.Lshortfile)
	// get the local zone and offset

	flag.BoolVar(&doTheDiscoMode, "disco-mode", false, "turn on disco mode")
	if tempV := strings.ToLower(os.Getenv("DISCO")); tempV != "" {
		if tempV == "true" || tempV == "1" {
			doTheDiscoMode = true
		} else {
			doTheDiscoMode = false
		}
	}
	flag.BoolVar(&doTheScrollMode, "scroll-mode", false, "turn on scroll mode")
	if tempV := strings.ToLower(os.Getenv("SCROLL")); tempV != "" {
		if tempV == "true" || tempV == "1" {
			doTheScrollMode = true
		} else {
			doTheScrollMode = false
		}
	}

	flag.BoolVar(&allZero, "off", false, "turn off all channels")

	hostname := os.Getenv("NAME")

	flag.BoolVar(&noMetrics, "no-metrics", false, "dont collect metrics")
	if tempV := strings.ToLower(os.Getenv("NO_METRICS")); tempV != "" {
		if tempV == "true" || tempV == "1" {
			noMetrics = true
		} else {
			noMetrics = false
		}
	}
	flag.BoolVar(&dummy, "dummy", false, "dont send conditions to light")
	if tempV := strings.ToLower(os.Getenv("DUMMY")); tempV != "" {
		if tempV == "true" || tempV == "1" {
			dummy = true
		} else {
			dummy = false
		}
	}
	flag.BoolVar(&absolute, "absolute", false, "use absolute light values in conditions file, not percentages")
	if tempV := strings.ToLower(os.Getenv("ABSOLUTE")); tempV != "" {
		if tempV == "true" || tempV == "1" {
			absolute = true
		} else {
			absolute = false
		}
	}

	flag.BoolVar(&loopFirstDay, "loop", false, "loop over the first day")
	if tempV := strings.ToLower(os.Getenv("LOOP")); tempV != "" {
		if tempV == "true" || tempV == "1" {
			loopFirstDay = true
		} else {
			loopFirstDay = false
		}
	}

	flag.StringVar(&hostTag, "host-tag", hostname, "host tag to add to the measurements")
	if tempV := os.Getenv("HOST_TAG"); tempV != "" {
		hostTag = tempV
	}

	flag.StringVar(&groupTag, "group-tag", "nonspc", "group tag to add to the measurements")
	if tempV := os.Getenv("GROUP_TAG"); tempV != "" {
		groupTag = tempV
	}

	flag.StringVar(&didTag, "did-tag", "", "deliverable id tag")
	if tempV := os.Getenv("DID_TAG"); tempV != "" {
		didTag = tempV
	}

	flag.StringVar(&conditionsPath, "conditions", "", "conditions file to run")
	if tempV := os.Getenv("CONDITIONS_FILE"); tempV != "" {
		conditionsPath = tempV
	}

	flag.DurationVar(&interval, "interval", time.Minute*10, "interval to run conditions/record metrics at")
	if tempV := os.Getenv("INTERVAL"); tempV != "" {
		interval, err = time.ParseDuration(tempV)
		if err != nil {
			errLog.Println("Couldnt parse interval from environment")
			errLog.Println(err)
		}
	}
	flag.Parse()
	if noMetrics && dummy {
		errLog.Println("dummy and no-metrics specified, nothing to do.")
		os.Exit(1)
	}
	if conditionsPath != "" && !dummy {
		chamber_tools.InitIndexConfig(errLog, conditionsPath)
	}

	errLog.Printf("timezone: \t%s\n", chamber_tools.ZoneName)
	errLog.Printf("hostTag: \t%s\n", hostTag)
	errLog.Printf("groupTag: \t%s\n", groupTag)
	errLog.Printf("file: \t%s\n", conditionsPath)
	errLog.Printf("interval: \t%s\n", interval)

}

func main() {
	gracefulStop := make(chan os.Signal)
	signal.Notify(gracefulStop, syscall.SIGTERM)
	signal.Notify(gracefulStop, syscall.SIGINT)


	// Set up options.
	// self.ser = serial.Serial('/dev/ttyUSB{}'.format(x), 9600, bytesize=serial.EIGHTBITS,
	// parity=serial.PARITY_NONE, stopbits=serial.STOPBITS_ONE,
	// rtscts=False, dsrdtr=False, xonxoff=False)

	//9600 baud, 8 bits, no parity, handshake off, free mode
	options := serial.OpenOptions{
		PortName:          flag.Arg(0),
		BaudRate:          9600,
		DataBits:          8,
		StopBits:          1,
		ParityMode:        serial.PARITY_NONE,
		MinimumReadSize:   4,
		RTSCTSFlowControl: true,
	}
	var err error
	port, err = serial.Open(options)

	if err != nil {
		panic(err)
	}
	go func() {
		sig := <-gracefulStop
		fmt.Printf("caught sig: %+v", sig)
		os.Exit(0)
	}()
	defer port.Close()
	if allZero {
		setAllZero()
	}
	if doTheDiscoMode {
		discoMode(100)
	}
	if doTheScrollMode {
		scrollMode(100)
	}

	if !dummy && conditionsPath != "" {
		go chamber_tools.RunConditions(errLog, runStuff, conditionsPath, loopFirstDay)
		select{}
	}
}
