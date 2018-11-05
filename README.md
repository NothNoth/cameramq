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


 ## AMQP

  - camera_stream : frames are emitted on this chan. ContentType matches with the frame type.
  - camera_ctrl : listens on this chan for config updates. Currently only receives "application/frameratems" with new frame rate