package main

import (
	"bytes"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"image/jpeg"
	"io/ioutil"
	"log"
	"os"
	"os/signal"
	"time"

	"github.com/blackjack/webcam"
	"github.com/streadway/amqp"
)

type CameraConfig struct {
	Device    string
	Encoding  string
	Format    uint32
	Width     uint32
	Height    uint32
	RefreshMs uint32
	RmqServer string
}

type CameraMQ struct {
	config        CameraConfig
	webcamHandler *webcam.Webcam
	conn          *amqp.Connection
	ch            *amqp.Channel
	ctrlQueue     amqp.Queue
	streamQueue   amqp.Queue
	killed        bool
}

func InitCameraMQ(configFile string) (*CameraMQ, error) {
	var cmq CameraMQ
	var err error
	cmq.killed = false

	//Load config
	data, err := ioutil.ReadFile(configFile)
	if err != nil {
		return nil, err
	}
	err = json.Unmarshal(data, &cmq.config)
	if err != nil {
		return nil, err
	}
	cmq.webcamHandler, err = webcam.Open(cmq.config.Device) // Open webcam
	if err != nil {
		return nil, err
	}

	_, _, _, err = cmq.webcamHandler.SetImageFormat(webcam.PixelFormat(cmq.config.Format), cmq.config.Width, cmq.config.Height)
	if err != nil {
		return nil, err
	}
	cmq.webcamHandler.SetAutoWhiteBalance(true)
	cmq.webcamHandler.SetBufferCount(2)
	err = cmq.webcamHandler.StartStreaming()
	if err != nil {
		return nil, err
	}

	//Setup AMQP
	cmq.conn, err = amqp.Dial(cmq.config.RmqServer)
	if err != nil {
		return nil, err
	}

	cmq.ch, err = cmq.conn.Channel()
	if err != nil {
		return nil, err
	}

	//Create control queue
	cmq.ctrlQueue, err = cmq.ch.QueueDeclare(
		"camera_ctrl", // name
		false,         // durable
		false,         // delete when unused
		false,         // exclusive
		false,         // no-wait
		nil,           // arguments
	)
	if err != nil {
		return nil, err
	}

	//Create Stream queue
	cmq.streamQueue, err = cmq.ch.QueueDeclare(
		"camera_stream", // name
		false,           // durable
		false,           // delete when unused
		false,           // exclusive
		true,            // no-wait
		nil,             // arguments
	)
	if err != nil {
		return nil, err
	}

	return &cmq, nil
}

func (cmq *CameraMQ) Destroy() {
	cmq.killed = true
	cmq.ch.Close()
	cmq.conn.Close()
	cmq.webcamHandler.StopStreaming()
	cmq.webcamHandler.Close()
}

func (cmq *CameraMQ) ReceiveCommands() error {
	msgs, err := cmq.ch.Consume(
		cmq.ctrlQueue.Name, // queue
		"",                 // consumer
		true,               // auto-ack
		false,              // exclusive
		false,              // no-local
		false,              // no-wait
		nil,                // args
	)
	if err != nil {
		return err
	}

	go func() {
		for d := range msgs {
			switch d.ContentType {
			case "application/frameratems":
				refreshMs := binary.BigEndian.Uint32(d.Body)
				log.Printf("Changing framerate to: %d ms", refreshMs)
				cmq.config.RefreshMs = refreshMs
			default:
				log.Printf("Received unexpected message: %s", d.Body)
			}
		}
	}()

	return nil
}

func (cmq *CameraMQ) EmitFrames() error {
	for {
		var frame []byte
		var err error
		time.Sleep(time.Duration(cmq.config.RefreshMs) * time.Millisecond)

		for {
			if cmq.killed == true {
				return nil
			}
			tmp, _ := cmq.webcamHandler.ReadFrame()
			if len(tmp) == 0 {
				continue
			}
			frame = make([]byte, len(tmp))
			copy(frame, tmp)
			break
		}

		switch cmq.config.Encoding {
		case "MJPEG":
			//Make sure we can decode JPEG
			rd := bytes.NewReader(frame)
			_, err = jpeg.Decode(rd)
			if err != nil {
				log.Print(err)
				continue
			}
			//JPEG is fine, emit on rmq chan
			cmq.sendFrame("jpeg", frame)
		default:
			fmt.Println("Unknown encoding: " + cmq.config.Encoding)
			return nil
		}

		if cmq.killed == true {
			return nil
		}
	}

	return nil
}

func (cmq *CameraMQ) sendFrame(format string, frame []byte) error {
	err := cmq.ch.Publish(
		"",                   // exchange
		cmq.streamQueue.Name, // routing key
		false,                // mandatory
		false,                // immediate
		amqp.Publishing{
			ContentType: fmt.Sprintf("image/%s", format),
			Body:        frame,
		})
	log.Printf("Sent %s frame (%dbytes)", format, len(frame))
	return err
}

func DumpCameras() {
	idx := 0
	for {
		dev := fmt.Sprintf("/dev/video%d", idx)
		cam, err := webcam.Open(dev) // Open webcam
		if err != nil {
			break
		}
		defer cam.Close()
		fmt.Printf("Device: %s\n", dev)

		//List video formats
		formatDesc := cam.GetSupportedFormats()
		for format, encoding := range formatDesc {

			//For given video format, get frame sizes
			frames := cam.GetSupportedFrameSizes(format)

			for res := 0; res < len(frames); res++ {
				fmt.Printf("  Format: %d Encoding: %s Width: %4d Height: %4d\n", format, encoding, uint32(frames[res].MaxWidth), uint32(frames[res].MaxHeight))
			}
		}

		idx++
	}
	fmt.Printf("\n%d devices found.\n", idx)
}

func main() {

	if len(os.Args) != 2 {
		fmt.Println("Usage: " + os.Args[0] + " <config file>")
		fmt.Println("")
		fmt.Println("*********** Supported Webcams ***********")
		DumpCameras()
		return
	}

	cmq, err := InitCameraMQ(os.Args[1])
	if err != nil {
		log.Fatalf("Failed to init CameraMQ: %s", err)
		return
	}
	defer cmq.Destroy()

	c := make(chan os.Signal, 1)
	signal.Notify(c, os.Interrupt)
	go func() {
		for sig := range c {
			fmt.Println(sig)
			cmq.killed = true
		}
	}()

	cmq.ReceiveCommands()
	cmq.EmitFrames()

	cmq.Destroy()
}
