package main

import (
	"fmt"
	"github.com/jacobsa/go-serial/serial"
	"time"
	"flag"
	"io"
	"math/rand"
	"math"
	"os"
	"log"
	"github.com/mdaffin/go-telegraf"
	"github.com/bcampbell/fuzzytime"
	"bufio"
	"strconv"
	"strings"
	"regexp"
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
	serviceChannel1 = 9
	serviceChannel2 = 15
)

const (
	channel0 = 0
	channel1 = 1
	channel2 = 3
	channel3 = 4
	channel4 = 5
	channel5 = 6
	channel6 = 7
	channel7 = 8

)

var availableChannels = []int{
	channel0,
	channel1,
	channel2,
	channel3,
	channel4,
	channel5,
	channel6,
	channel7,
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
	errLog *log.Logger
	ctx fuzzytime.Context
	zoneName string
	zoneOffset int
)


var (
	doTheDiscoMode bool
	doTheScrollMode bool
	allZero bool
	noMetrics, dummy, absolute bool
	conditionsPath, hostTag   string
	interval         time.Duration
)

var port io.ReadWriteCloser

const (
	matchFloatExp = `[-+]?\d*\.\d+|\d+`
	matchIntsExp = `\b(\d+)\b`
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

func setOne(port io.ReadWriteCloser, lightChannel, value int) ( err error ){
	intensityPackt, err := SetIntensityPacket(lightChannel, value)
	if err != nil{
		errLog.Println(err)
		return
	}
	activatePackt, err := ActivatePacket(lightChannel)
	if err != nil{
		errLog.Println(err)
		return
	}

	port.Write(intensityPackt)
	port.Write(activatePackt)
	return nil
}



func setMany(port io.ReadWriteCloser, values []int) ( err error ){
	for i := 0; i < len(values) && i < len(availableChannels); i ++{
		lightChannel := availableChannels[i]
		value := values[i]
		if value < 0 {
			return nil
		}
		intensityPackt, err := SetIntensityPacket(lightChannel, value)

		if err != nil{
			errLog.Println(err)
			return err
		}

		activatePackt, err := ActivatePacket(lightChannel)
		if err != nil{
			errLog.Println(err)
			return err
		}

		port.Write(intensityPackt)
		port.Write(activatePackt)
	}

	return nil
}


func scrollMode(max int){
	if max > 1022 {
		max = 1022
	}
	setAllZero()

	scrollOne := func (lightChannel int){
		fmt.Println("Scrolling channel ", lightChannel)
		for value := -math.Pi/2; value <= 1.5*math.Pi; value += 0.1 {
			sinV := (1 + math.Sin(value))/2
			thisV := sinV * float64(max)
			setOne(port, lightChannel, int(math.Round(thisV)))
			time.Sleep(100*time.Millisecond)
		}
	}

	for {
		for _, lightChannel := range availableChannels {
			scrollOne(lightChannel)
		}
		scrollOne(serviceChannel1)
		time.Sleep(1*time.Second)
	}
}

func setAllRandom(port io.ReadWriteCloser, max int){
	for i := 1; i < len(availableChannels); i++{
		lightChannel := availableChannels[i]
		value := random(max)
		intensityPackt, err := SetIntensityPacket(lightChannel, value)
		if err != nil{
			errLog.Println(err)
		}
		activatePackt, err := ActivatePacket(lightChannel)
		if err != nil{
			errLog.Println(err)
		}
		port.Write(intensityPackt)
		port.Write(activatePackt)
	}
}


func parseDateTime(tString string) (time.Time, error) {
	datetimeValue, _, err := ctx.Extract(tString)
	if err != nil {
		errLog.Printf("couldn't extract datetime: %s", err)
	}
	datetimeValue.Time.SetHour(datetimeValue.Time.Hour())
	datetimeValue.Time.SetMinute(datetimeValue.Time.Minute())
	datetimeValue.Time.SetSecond(datetimeValue.Time.Second())
	datetimeValue.Time.SetTZOffset(zoneOffset)

	return time.Parse("2006-01-02T15:04:05Z07:00", datetimeValue.ISOFormat())
}

// max returns the larger of x or y.
func max(x, y int) int {
	if x < y {
		return y
	}
	return x
}

// min returns the smaller of x or y.
func min(x, y int) int {
	if x > y {
		return y
	}
	return x
}

func writeMetrics(lightValues []int) error {
	if !noMetrics{
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
		for i,v := range lightValues{
			m.AddInt(fmt.Sprintf("chan-%d", i), v)
		}
		if hostTag != "" {
			m.AddTag("host", hostTag)
		}
		telegrafClient.Write(m)
	}
	return nil
}

// runStuff, should send values and write metrics.
// returns true if program should continue, false if program should retry
func runStuff(theTime time.Time, lineSplit []string) bool {
	stringVals := lineSplit[4:]
	lightValues := make([]int, len(stringVals))

	for i, v := range stringVals {
		found := matchFloat.FindString(v)
		if len(found) < 0{
			errLog.Printf("couldnt parse %s as float.\n", v)
			continue
		}
		fl, err := strconv.ParseFloat(found, 64)
		if err != nil{
			errLog.Println(err)
			continue
		}
		if !absolute{
			// convert from percentage if we are not using absolute values.
			fl = fl * 10.22
		}
		lightValues[i] = int(math.Round(fl))
	}

	setMany(port, lightValues)

	errLog.Println("ran ", theTime.Format("2006-01-02T15:04:05"), lightValues)

	for x := 0; x < 5; x++ {
		if err := writeMetrics(lightValues); err != nil{
			errLog.Println(err)
			time.Sleep(200 * time.Millisecond)
			continue
		}
		break
	}
	return true
}

func runConditions() {
	errLog.Printf("running conditions file: %s\n", conditionsPath)
	file, err := os.Open(conditionsPath)
	if err != nil {
		log.Fatal(err)
	}
	defer file.Close()
	scanner := bufio.NewScanner(file)
	idx := 0
	var lastTime time.Time
	var lastLineSplit []string
	firstRun := true
	for scanner.Scan() {
		line := scanner.Text()
		if idx == 0 {
			idx++
			continue
		}

		lineSplit := strings.Split(line, ",")
		timeStr := lineSplit[0]
		theTime, err := parseDateTime(timeStr)
		if err != nil {
			errLog.Println(err)
			continue
		}

		// if we are before the time skip until we are after it
		// the -10s means that we shouldnt run again.
		if theTime.Before(time.Now()){
			lastLineSplit = lineSplit
			lastTime = theTime
			continue
		}

		if firstRun {
			firstRun = false
			errLog.Println("running firstrun line")
			for i:=0; i < 10; i++{
				if runStuff(lastTime, lastLineSplit) {
					break
				}
			}
		}

		errLog.Printf("sleeping for %ds\n",int(time.Until(theTime).Seconds()))
		time.Sleep(time.Until(theTime))

		// RUN STUFF HERE
		for i:=0; i < 10; i++{
			if runStuff(theTime, lineSplit) {
				break
			}
		}
		// end RUN STUFF
		idx++
	}
}

func setAllZero(){
	activatePackt, err := DeActivatePacket(serviceChannel1)
		if err != nil{
			errLog.Println(err)
		}
	port.Write(activatePackt)
}

func discoMode(max int){
	ticker := time.NewTicker(500* time.Millisecond)
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

	errLog = log.New(os.Stderr, "[fytopanel] ", log.Ldate|log.Ltime|log.Lshortfile)
	// get the local zone and offset
	zoneName, zoneOffset = time.Now().Zone()
	errLog.Printf("timezone: %s\n", zoneName)

	ctx = fuzzytime.Context{
		DateResolver: fuzzytime.DMYResolver,
		TZResolver:   fuzzytime.DefaultTZResolver(zoneName),
	}

	flag.BoolVar(&doTheDiscoMode, "disco-mode", false, "turn on disco mode")

	flag.BoolVar(&doTheScrollMode, "scroll-mode", false, "turn on scroll mode")
	flag.BoolVar(&allZero, "off", false, "turn off all channels")

	flag.BoolVar(&noMetrics, "no-metrics", false, "dont collect metrics")
	flag.BoolVar(&dummy, "dummy", false, "dont send conditions to light")
	flag.BoolVar(&absolute, "absolute", false, "use absolute light values in conditions file, not percentages")
	flag.StringVar(&hostTag, "host-tag", "", "host tag to add to the measurements")
	flag.StringVar(&conditionsPath, "conditions", "", "conditions file to")
	flag.DurationVar(&interval, "interval", time.Minute*10, "interval to run conditions/record metrics at")
	flag.Parse()
	if noMetrics && dummy {
		errLog.Println("dummy and no-metrics specified, nothing to do.")
		os.Exit(1)
	}
}



func main() {
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

	if allZero{
		setAllZero()
	}
	if doTheDiscoMode {
		discoMode(100)
	}
	if doTheScrollMode {
		scrollMode(100)
	}

	if !dummy && conditionsPath != "" {
		runConditions()
	}
}