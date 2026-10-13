//! A tiny scripted HTTP/1.1 server for the client tests: in process, on a
//! loopback port in the Field cycle's range (56200 to 56299), one thread that
//! ends with the test. Each accepted connection reads one request, records
//! it, answers the next scripted reply and closes. No dependency, nothing
//! detached: `finish` stops the thread and returns what it saw.

use std::{
    io::{Read, Write},
    net::{TcpListener, TcpStream},
    sync::{
        atomic::{AtomicBool, Ordering},
        Arc, Mutex,
    },
    thread::JoinHandle,
    time::{Duration, Instant},
};

pub const PORT_FIRST: u16 = 56200;
pub const PORT_LAST: u16 = 56299;

/// One scripted answer.
#[derive(Clone, Debug)]
pub struct Reply {
    pub status: u16,
    pub headers: Vec<(String, String)>,
    pub body: Vec<u8>,
    /// Accept the connection and never answer (a client timeout case).
    pub hang: bool,
}

impl Reply {
    pub fn status(status: u16) -> Self {
        Self { status, headers: Vec::new(), body: Vec::new(), hang: false }
    }

    pub fn json(status: u16, body: &str) -> Self {
        Self::status(status).header("Content-Type", "application/json").body(body.as_bytes())
    }

    pub fn header(mut self, name: &str, value: &str) -> Self {
        self.headers.push((name.to_string(), value.to_string()));
        self
    }

    pub fn body(mut self, body: &[u8]) -> Self {
        self.body = body.to_vec();
        self
    }

    pub fn hang() -> Self {
        Self { hang: true, ..Self::status(0) }
    }
}

/// What the server read for one request.
#[derive(Clone, Debug)]
pub struct Seen {
    pub method: String,
    pub path: String,
    /// Header names lower cased.
    pub headers: Vec<(String, String)>,
    pub body: String,
}

impl Seen {
    pub fn header(&self, name: &str) -> Option<&str> {
        let name = name.to_ascii_lowercase();
        self.headers.iter().find(|(n, _)| *n == name).map(|(_, v)| v.as_str())
    }
}

pub struct MockServer {
    port: u16,
    seen: Arc<Mutex<Vec<Seen>>>,
    stop: Arc<AtomicBool>,
    handle: Option<JoinHandle<()>>,
}

/// Ports handed out as "closed": no server in this process may take them, so
/// a parallel test cannot start listening where another expects a refusal.
static CLOSED: Mutex<Vec<u16>> = Mutex::new(Vec::new());

fn bind_in_range() -> TcpListener {
    let closed = CLOSED.lock().unwrap();
    for port in PORT_FIRST..=PORT_LAST {
        if closed.contains(&port) {
            continue;
        }
        if let Ok(l) = TcpListener::bind(("127.0.0.1", port)) {
            return l;
        }
    }
    panic!("no free port in 56200 to 56299");
}

/// A port in the range with nothing listening (connection refused), reserved
/// for the rest of the process.
pub fn closed_port() -> u16 {
    let mut closed = CLOSED.lock().unwrap();
    for port in PORT_FIRST..=PORT_LAST {
        if closed.contains(&port) {
            continue;
        }
        if let Ok(l) = TcpListener::bind(("127.0.0.1", port)) {
            drop(l);
            closed.push(port);
            return port;
        }
    }
    panic!("no free port in 56200 to 56299");
}

fn read_request(stream: &mut TcpStream) -> Option<Seen> {
    stream.set_read_timeout(Some(Duration::from_secs(5))).ok()?;
    let mut buf = Vec::new();
    let mut chunk = [0u8; 2048];
    let head_end = loop {
        if let Some(i) = buf.windows(4).position(|w| w == b"\r\n\r\n") {
            break i;
        }
        let n = stream.read(&mut chunk).ok()?;
        if n == 0 {
            return None;
        }
        buf.extend_from_slice(&chunk[..n]);
    };
    let head = String::from_utf8_lossy(&buf[..head_end]).to_string();
    let mut lines = head.split("\r\n");
    let mut first = lines.next()?.split(' ');
    let method = first.next()?.to_string();
    let path = first.next()?.to_string();
    let headers: Vec<(String, String)> = lines
        .filter_map(|l| l.split_once(':'))
        .map(|(n, v)| (n.trim().to_ascii_lowercase(), v.trim().to_string()))
        .collect();
    let want: usize = headers
        .iter()
        .find(|(n, _)| n == "content-length")
        .and_then(|(_, v)| v.parse().ok())
        .unwrap_or(0);
    let mut body = buf[head_end + 4..].to_vec();
    while body.len() < want {
        let n = stream.read(&mut chunk).ok()?;
        if n == 0 {
            break;
        }
        body.extend_from_slice(&chunk[..n]);
    }
    Some(Seen { method, path, headers, body: String::from_utf8_lossy(&body).to_string() })
}

fn write_reply(stream: &mut TcpStream, reply: &Reply) {
    let mut out = format!("HTTP/1.1 {} X\r\nContent-Length: {}\r\nConnection: close\r\n", reply.status, reply.body.len());
    for (n, v) in &reply.headers {
        out.push_str(&format!("{n}: {v}\r\n"));
    }
    out.push_str("\r\n");
    let _ = stream.write_all(out.as_bytes());
    let _ = stream.write_all(&reply.body);
    let _ = stream.flush();
}

impl MockServer {
    /// Serve `script` in order, one reply per connection; further connections
    /// get a 500. Ends at `finish`.
    pub fn start(script: Vec<Reply>) -> Self {
        let listener = bind_in_range();
        listener.set_nonblocking(true).unwrap();
        let port = listener.local_addr().unwrap().port();
        let seen = Arc::new(Mutex::new(Vec::new()));
        let stop = Arc::new(AtomicBool::new(false));
        let (seen2, stop2) = (seen.clone(), stop.clone());
        let handle = std::thread::spawn(move || {
            let mut script = script.into_iter();
            let mut hung: Vec<TcpStream> = Vec::new();
            let deadline = Instant::now() + Duration::from_secs(30);
            while !stop2.load(Ordering::SeqCst) && Instant::now() < deadline {
                match listener.accept() {
                    Ok((mut stream, _)) => {
                        stream.set_nonblocking(false).ok();
                        let Some(req) = read_request(&mut stream) else { continue };
                        seen2.lock().unwrap().push(req);
                        let reply = script.next().unwrap_or_else(|| Reply::status(500));
                        if reply.hang {
                            hung.push(stream);
                        } else {
                            write_reply(&mut stream, &reply);
                        }
                    }
                    Err(_) => std::thread::sleep(Duration::from_millis(5)),
                }
            }
        });
        Self { port, seen, stop, handle: Some(handle) }
    }

    pub fn url(&self) -> String {
        format!("http://127.0.0.1:{}", self.port)
    }

    /// Stop the server thread and return every request it read, in order.
    pub fn finish(mut self) -> Vec<Seen> {
        self.stop.store(true, Ordering::SeqCst);
        if let Some(h) = self.handle.take() {
            let _ = h.join();
        }
        self.seen.lock().unwrap().clone()
    }
}

impl Drop for MockServer {
    fn drop(&mut self) {
        self.stop.store(true, Ordering::SeqCst);
        if let Some(h) = self.handle.take() {
            let _ = h.join();
        }
    }
}
