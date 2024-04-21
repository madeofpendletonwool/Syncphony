use libmdns::{Responder, Service};
use rodio::{OutputStream, Decoder, Sink};
use std::io::Cursor;
use tokio::net::TcpListener;
use tokio::io::AsyncReadExt;

#[tokio::main]
async fn main() -> std::io::Result<()> {
    let responder = Responder::new().unwrap();
    let service = responder.register("_http._tcp".to_owned(), "RustPodcastClient".to_owned(), 8080, &["path=/"]);
    let listener = TcpListener::bind("0.0.0.0:8080").await?;
    println!("Server running on port 8080");

    loop {
        let (mut socket, _) = listener.accept().await?;

        // Moved inside the main loop directly, no tokio::spawn
        let (_stream, handle) = OutputStream::try_default().unwrap();
        let sink = Sink::try_new(&handle).unwrap();
        let mut buffer = Vec::new();
        let mut temp_buffer = Vec::new();

        // Read data into a temporary buffer
        while let Ok(n) = socket.read_buf(&mut temp_buffer).await {
            if n == 0 { break; } // Exit if socket is closed
            buffer.extend_from_slice(&temp_buffer);
            temp_buffer.clear();

            // Check if we have enough data to start playback
            if buffer.len() > 10000000 { // Adjusted threshold for actual needs
                let cursor = Cursor::new(buffer.clone());
                if let Ok(source) = Decoder::new(cursor) {
                    sink.append(source);
                }
                buffer.clear();
            }
        }

        // Ensure playback finishes before dropping Sink and OutputStream
        sink.sleep_until_end();
    }
}
