# CameraMQ

A rabbitMQ webcam streamer

## Config

    {
      "Device": "/dev/video0", <-- device used for capture
      "Encoding": "MJPEG",     <-- encoding type
      "Format": 1196444237,    <-- v4l encoding format
      "Width": 1280,           <-- frame width
      "Height": 720,           <-- frame height
      "RefreshMs": 500         <-- capture and emit frame delay
    }

Run cameramq with no arguments to dump detected available cameras and pick proper one to setup your config file.

## AMQP


| AMQP channel | IN/OUT | Content-Type | Data | Description |
| ------------ | ------ | ------------ | ---- | ----------- |
| camera_stream | OUT   | image/jpeg   | JPEG | Captured frame. Content-Type matches with the type of the picture found in data |
| camera_ctrl   | IN    |application/frameratems | New frame rate in ms (int32) | Changes the capture frame rate  |
