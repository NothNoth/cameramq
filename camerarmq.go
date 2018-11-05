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
}

type CameraMQ struct {
	config        CameraConfig
	webcamHandler *webcam.Webcam
	conn          *amqp.Connection
	ch            *amqp.Channel
	ctrlQueue     amqp.Queue
	streamQueue   amqp.Queue
}

func InitCameraMQ(configFile string) (*CameraMQ, error) {
	var cmq CameraMQ
	var err error

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

	err = cmq.webcamHandler.StartStreaming()
	if err != nil {
		return nil, err
	}

	//Setup AMQP
	cmq.conn, err = amqp.Dial("amqp://guest:guest@localhost:5672/")
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
		false,           // no-wait
		nil,             // arguments
	)
	if err != nil {
		return nil, err
	}

	return &cmq, nil
}

func (cmq *CameraMQ) Destroy() {
	cmq.ch.Close()
	cmq.conn.Close()

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
		time.Sleep(time.Duration(cmq.config.RefreshMs) * time.Millisecond)
		err := cmq.webcamHandler.WaitForFrame(1)

		switch err.(type) {
		case nil:
		case *webcam.Timeout:
			log.Print("Timeout: " + err.Error())
			continue
		default:
			log.Print("WaitForFrame failed: " + err.Error())
			continue
		}

		frame, err := cmq.webcamHandler.ReadFrame()
		if err != nil {
			log.Print("ReadFrame failed: " + err.Error())
			continue
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
	}
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

	cmq.ReceiveCommands()
	cmq.EmitFrames()
}
