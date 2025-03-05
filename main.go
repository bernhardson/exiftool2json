package main

import (
	"encoding/json"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"os/exec"
)

type Table struct {
	XMLName xml.Name `xml:"table"`
	Name    string   `xml:"name,attr"`
	Tags    []Tag    `xml:"tag"`
}

type Tag struct {
	XMLName  xml.Name `xml:"tag"`
	ID       string   `xml:"id,attr"`
	Name     string   `xml:"name,attr"`
	Type     string   `xml:"type,attr"`
	Writable bool     `xml:"writable,attr"`
	Group    string   `xml:"g2,attr"`
	Desc     []Desc   `xml:"desc"`
}

type Desc struct {
	Lang  string `xml:"lang,attr"`
	Value string `xml:",chardata"`
}

type JSONTag struct {
	Writable    bool              `json:"writable"`
	Path        string            `json:"path"`
	Group       string            `json:"group"`
	Description map[string]string `json:"description"`
	Type        string            `json:"type"`
}

func streamTags(w http.ResponseWriter, r *http.Request, tableName, tagName string) error {
	cmd := exec.Command("exiftool", "-listx")
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return fmt.Errorf("failed to create stdout pipe: %w", err)
	}
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("failed to start exiftool: %w", err)
	}
	defer cmd.Wait()
	defer cmd.Process.Kill()

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	w.Write([]byte("{\"tags\": ["))
	flusher, ok := w.(http.Flusher)
	if !ok {
		return fmt.Errorf("streaming not supported")
	}

	decoder := xml.NewDecoder(stdout)
	encoder := json.NewEncoder(w)
	first := true

	for {
		select {
		case <-r.Context().Done():
			cmd.Process.Kill()
			return errors.New("client closed connection")
		default:
		}

		tok, err := decoder.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			return fmt.Errorf("error reading XML: %w", err)
		}

		if se, ok := tok.(xml.StartElement); ok {
			if se.Name.Local == "table" {
				var table Table
				if err := decoder.DecodeElement(&table, &se); err != nil {
					return fmt.Errorf("error decoding table: %w", err)
				}
				if table.Name != tableName && tableName != "" {
					continue
				}
				for _, tag := range table.Tags {
					if tag.Name != tagName && tagName != "" {
						continue
					}
					descMap := make(map[string]string)
					for _, desc := range tag.Desc {
						descMap[desc.Lang] = desc.Value
					}

					jsonTag := JSONTag{
						Writable:    tag.Writable,
						Path:        fmt.Sprintf("%s:%s", table.Name, tag.Name),
						Group:       table.Name,
						Description: descMap,
						Type:        tag.Type,
					}

					if !first {
						w.Write([]byte(","))
					} else {
						first = false
					}

					if err := encoder.Encode(jsonTag); err != nil {
						return fmt.Errorf("error encoding JSON: %w", err)
					}
					flusher.Flush()
				}
			}
		}
	}
	w.Write([]byte("]}"))
	flusher.Flush()
	return nil
}

func tagsHandler(w http.ResponseWriter, r *http.Request) {
	tableName := r.URL.Query().Get("table")
	tagName := r.URL.Query().Get("tag")
	// from the specs I considered that the table name is required, since we have a list of tags for a specific table
	// for the same reason the tag is optional
	//if tableName == "" {
	//	http.Error(w, "table name required", http.StatusBadRequest)
	//		return
	//	}
	if err := streamTags(w, r, tableName, tagName); err != nil {
		if err.Error() == "client closed connection" {
			// added a log message to know when the client closes the connection
			// since the http request context is already done and was throwing an ambiguous error message
			fmt.Println("Client closed connection")
		}
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

func main() {
	http.HandleFunc("/tags", tagsHandler)
	log.Println("Server started on :8080")
	log.Fatal(http.ListenAndServe(":8080", nil))
}
