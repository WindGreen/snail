package main

import (
	"bytes"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"net"
	"os"
	"strconv"
	"strings"
	"time"
)

var find, output, port, proxy, input, rewrite string
var debug, cut, compare, combine bool

func main() {
	flag.StringVar(&find, "find", "", "sub string in uri")
	flag.StringVar(&output, "output", "", "file name")
	flag.StringVar(&input, "input", "", "file name")
	flag.StringVar(&port, "port", "8080", "listen port")
	flag.StringVar(&proxy, "proxy", "", "proxy address")
	flag.StringVar(&rewrite, "rewrite", "", "header_name:header_value")
	flag.BoolVar(&debug, "debug", false, "debug mode")
	flag.BoolVar(&cut, "cut", false, "cut file")
	flag.BoolVar(&compare, "compare", false, "compare files")
	flag.BoolVar(&combine, "combine", false, "combine files")
	flag.Parse()

	if cut {
		cutFile()
		return
	} else if compare {
		compareFile()
		return
	} else if combine {
		combineFile()
		return
	}

	listener, err := net.Listen("tcp", ":"+port)
	if err != nil {
		log.Fatal(err)
	}
	defer listener.Close()
	log.Println("listening", port)

	if proxy != "" {
		log.Println("via proxy", proxy)
	}

	for {
		conn, err := listener.Accept()
		if err != nil {
			log.Println(err)
			continue
		}
		go handleConn(conn)
	}
}

func handleConn(c net.Conn) {
	defer c.Close()

	found := false
	buf := make([]byte, 32*1024)
	nr, err := c.Read(buf)
	if err != nil {
		log.Println(err)
		return
	}

	var method, uri, addr, host, scheme, p string
	_, err = fmt.Sscanf(string(buf[:bytes.IndexByte(buf[:], '\n')]), "%s%s", &method, &uri)
	if find != "" && strings.Contains(uri, find) {
		log.Println("found", uri)
		found = true
	}
	if debug {
		log.Println(method, uri)
	}
	if proxy == "" {
		u := uri
		if i := strings.Index(u, "://"); i > 0 {
			scheme = uri[:i]
			u = u[i+3:]
		}
		if i := strings.Index(u, ":"); i > 0 {
			host = u[:i]
			u = u[i+1:]
			if j := strings.Index(u, "/"); j > 0 {
				p = u[:j]
			} else {
				p = u[:]
			}
		} else if i := strings.Index(u, "/"); i > 0 {
			host = u[:i]
			p = "80"
		} else {
			host = u[:]
			if scheme == "https" {
				port = "443"
			} else {
				p = "80"
			}
		}
		addr = host + ":" + p
	} else {
		addr = proxy
	}
	if found && rewrite != "" {
		header := strings.SplitN(rewrite, ":", 2)
		log.Println(header[0], header[1])
		if len(header) != 2 {
			log.Println("rewrite failed, header is not supported")
		} else {
			s := string(buf[:nr])
			ss := strings.Split(s, "\r\n")
			for i, t := range ss {
				if strings.Contains(t, header[0]) {
					log.Println("rewrite before:", ss[i])
					ss[i] = rewrite
					log.Println("rewrite after:", ss[i])
				}
			}
			s = strings.Join(ss, "\r\n")
			log.Println("before length", nr)
			nr = len(s)
			log.Println("after length", nr)
			buf = []byte(s)
		}
	}

	s, err := net.Dial("tcp", addr)
	if err != nil {
		log.Println(err)
		return
	}
	if method == "CONNECT" {
		fmt.Fprint(c, "HTTP/1.1 200 Connection established\r\n\r\n")
	} else {
		s.Write(buf[:nr])
	}
	if debug {
		log.Println("connect to", addr)
	}

	go func() {
		if !found {
			io.Copy(c, s)
		} else {
			filename := output
			if filename == "" {
				filename = time.Now().Format("20060102_150405.000000000")
			}
			f, err := os.Create(filename)
			if err != nil {
				panic(err)
			}
			defer f.Close()
			nw, err := copyBuffer(s, c, f)
			if err != nil {
				log.Println("s->c:", err)
			}
			log.Printf("copy %d bytes\n", nw)
		}
	}()
	io.Copy(s, c)
}

func copyBuffer(src io.Reader, dst ...io.Writer) (written int64, err error) {
	tmp := make([]byte, 32*1024)
	for {
		nr, er := src.Read(tmp)
		if nr > 0 {
			for _, d := range dst {
				nw, ew := d.Write(tmp[0:nr])
				if ew != nil {
					err = ew
					break
				}
				if nr != nw {
					err = io.ErrShortWrite
					break
				}
			}
		}
		if nr > 0 {
			written += int64(nr)
		}
		if er != nil {
			if er != io.EOF {
				err = er
			}
			break
		}
	}
	return written, err
}

func cutFile() {
	in, err := os.Open(input)
	if err != nil {
		panic(err)
	}
	defer in.Close()
	out, err := os.Create(output)
	if err != nil {
		panic(err)
	}
	defer out.Close()
	first := true
	buf := make([]byte, 32*1024)
	for {
		ir, err := in.Read(buf)
		if ir > 0 {
			if first {
				if i := bytes.Index(buf, []byte("\r\n\r\n")); i > 0 {
					buf = buf[i+4:]
					ir -= i + 4
				}
			}
			ow, err := out.Write(buf[:ir])
			if err != nil {
				log.Println("wirte error", err)
				break
			}
			if ir != ow {
				log.Println("write length not equal to read length")
				break
			}
		}
		if err != nil {
			if err != io.EOF {
				log.Println("read failed", err)
			}
			break
		}
		first = false
	}
}

func compareFile() {
	files := strings.Split(input, ",")
	if len(files) != 2 {
		log.Println("two files name should seperated by comma")
		return
	}
	in1, err := os.Open(files[0])
	if err != nil {
		panic(err)
	}
	defer in1.Close()
	in2, err := os.Open(files[1])
	if err != nil {
		panic(err)
	}
	defer in2.Close()
	offset := 0
	buf1 := make([]byte, 32*1024)
	buf2 := make([]byte, 32*1024)
	for {
		ir1, err1 := in1.Read(buf1)
		ir2, err2 := in2.Read(buf2)
		if ir1 != ir2 {
			log.Println("length not equal")
			break
		}
		for i := 0; i < ir1; i++ {
			if buf1[i] != buf2[i] {
				log.Printf("index %d not equal:%x,%x", offset+i, buf1[i], buf2[i])
				fmt.Printf("%x\n%x\n", buf1[:100], buf2[:100])
				break
			}
		}
		if err1 != nil || err2 != nil {
			break
		}
		offset += ir1
	}
	log.Println("equal")
}

func combineFile() {
	files := strings.Split(input, ",")
	if len(files) < 2 {
		log.Println("files's name should seperated by comma")
		return
	}
	outfile, err := os.Create(output)
	if err != nil {
		log.Println("create file failed", err)
		return
	}
	defer outfile.Close()

	for i, file := range files {
		s := 0
		e := -1
		name := ""
		if j := strings.Index(file, "["); j > 0 {
			name = file[:j]
			k := strings.Index(file, "]")
			l := strings.Index(file, ":")
			if k < j || l < j || l > k {
				log.Println("wrong file name")
				break
			}
			var err error
			if l-j > 1 {
				s, err = strconv.Atoi(file[j+1 : l])
				if err != nil {
					log.Println("wrong index:", s)
					break
				}
			}
			if k-l > 1 {
				e, err = strconv.Atoi(file[l+1 : k])
				if err != nil {
					log.Println("wrong index:", s)
					break
				}
			}
		} else {
			name = file
		}
		n, err := copyPart(name, int64(s), int64(e-s), outfile)
		if err != nil {
			log.Printf("[%d]copy failed:%s", i, err)
			break
		}
		log.Printf("[%d]copy %s->%s: %d bytes", i, name, output, n)
	}
}

func copyPart(infile string, start, length int64, outfile io.Writer) (written int64, err error) {
	file, err := os.Open(infile)
	if err != nil {
		return
	}
	defer file.Close()
	if length < 0 {
		fileinfo, er := file.Stat()
		if err != nil {
			err = er
			return
		}
		length = fileinfo.Size()
	}
	var offset int64
	buf := make([]byte, 32*1024)
	if start == -1 {
		start = 0
	}
	offset += start
	for {
		nr, er := file.ReadAt(buf, int64(offset))
		stop := int64(nr)
		if nr > 0 {
			if written+stop > length {
				stop = length - written
				if stop < 0 {
					err = errors.New("written is bigger than length")
					break
				}
			}
			nw, ew := outfile.Write(buf[:stop])
			written += int64(nw)
			if ew != nil {
				err = ew
				break
			}
			if int64(nw) != stop {
				err = io.ErrShortWrite
				break
			}
		}
		if er != nil {
			if er != io.EOF {
				err = er
			}
			break
		}
		offset += stop
	}
	return
}
