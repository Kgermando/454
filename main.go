package main

import (
	"bytes"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"log"
	"net"
	"net/http"
	"os"
	"regexp"
	"time"

	"github.com/go-chi/chi"
	"github.com/go-chi/cors"
	_ "github.com/glebarez/go-sqlite"
)

var totalRequests int

// this is for anyone trying to be sneaky and store files by base64 encoding them
// (this was very painful to write)
var b64DataRegex = regexp.MustCompile("^data:.+;base64,")
var fileHeaders = [][]byte{
	{0x42, 0x4d},                         // .bmp
	{0x53, 0x49, 0x4d, 0x50, 0x4c, 0x45}, // .fits
	{0x47, 0x49, 0x46, 0x38},             // .gif
	{0x47, 0x4b, 0x53, 0x4d},             // .gks
	{0x01, 0xda},                         // .rgb
	{0xf1, 0x00, 0x40, 0xbb},             // .itc
	{0xff, 0xd8, 0xff, 0xe0},             // .jpg
	{0x49, 0x49, 0x4e, 0x31},             // .nif
	{0x56, 0x49, 0x45, 0x57},             // .pm (not PenguinMod lol)
	{0x89, 0x50, 0x4e, 0x47},             // .png
	{0x25, 0x21},                         // .[e]ps
	{0x59, 0xa6, 0x6a, 0x95},             // .ras
	{0x4d, 0x4d, 0x00, 0x2a},             // .tif (Motorola)
	{0x49, 49, 0x2a, 0x00},               // .tif (Intel)
	{0x67, 0x69, 0x6d, 0x70, 0x20, 0x78, 0x63, 0x66, 0x20, 0x76}, // .xcf
	{0x23, 0x46, 0x49, 0x47},                                     // .fig
	{0x2f, 0x2a, 0x20, 0x58, 0x50, 0x4d, 0x20, 0x2a, 0x2f},       // .xpm
	{0x42, 0x5a},                   // .bz
	{0x1f, 0x9d},                   // .Z
	{0x1f, 0x8b},                   // .gz
	{0x50, 0x4b, 0x03, 0x04},       // .zip
	{0x75, 0x73, 0x74, 0x61, 0x72}, // .tar
	{0x4d, 0x5a},                   // .exe
	{0x7f, 0x45, 0x4c, 0x46},       // .elf
	{0xca, 0xfe, 0xba, 0xbe},       // .class
	{0x00, 0x00, 0x01, 0x00},       // .ico
	{0x52, 0x49, 0x46, 0x46},       // .avi
	{0x46, 0x57, 0x53},             // .swf
	{0x46, 0x4c, 0x56},             // .flv
	{0x00, 0x00, 0x00, 0x18, 0x66, 0x74, 0x79, 0x70, 0x6d, 0x70, 0x34, 0x32}, // .mp4
	{0x6d, 0x6f, 0x6f, 0x76},                         // .mov
	{0x30, 0x26, 0xb2, 0x75, 0x8e, 0x66, 0xcf},       // .wmv/.wma
	{0xd0, 0xcf, 0x11, 0xe0, 0xa1, 0xb1, 0x1a, 0xe1}, // .msi/.doc/.msg
	{0x4c, 0x01},             // .obj
	{0x4d, 0x5a},             // .dll
	{0x4d, 0x53, 0x43, 0x46}, // .cab
	{0x52, 0x61, 0x72, 0x21, 0x1a, 0x07, 0x00},                   // .rar
	{0x25, 0x50, 0x44, 0x46},                                     // .pdf
	{0x50, 0x4b, 0x03, 0x04},                                     // .docx
	{0x50, 0x4b, 0x03, 0x04, 0x14, 0x00, 0x08, 0x00, 0x08, 0x00}, // .jar
	{0x78, 0x9c}, // .zlib
}
var timedOutIPs = map[string]int64{}

func includesFile(input string) bool {
	// Remove the data URI prefix if present
	b64String := string(b64DataRegex.ReplaceAll([]byte(input), []byte("")))

	// Attempt to base64 decode string
	decoded, err := base64.StdEncoding.DecodeString(b64String)
	if err != nil {
		return false
	}

	// Check against file headers
	for i := 0; i < len(fileHeaders); i++ {
		if bytes.HasPrefix(decoded, fileHeaders[i]) {
			log.Println("File detected with header: " + string(fileHeaders[i]))
			return true
		}
	}

	return false
}

func jsonError(w http.ResponseWriter, errStr string, status int) {
	jsonified, err := json.Marshal(struct {
		Error string `json:"error"`
	}{
		Error: errStr,
	})
	if err != nil {
		log.Println(err)
		http.Error(w, "Internal server error", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	w.Write(jsonified)
}

func jsonSuccess(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	w.Write([]byte(`{"success": true}`))
}

func main() {
		// Initialise database connection
	log.Println("Path is "+os.Getenv("RAILWAY_VOLUME_MOUNT_PATH"))
	log.Println("DB file at "+os.Getenv("RAILWAY_VOLUME_MOUNT_PATH")+"/db.sqlite")
	db, err := sql.Open("sqlite", os.Getenv("RAILWAY_VOLUME_MOUNT_PATH")+"/db.sqlite")
	if err != nil {
		log.Fatalln(err)
	}

	// Test database connection
	if err := db.Ping(); err != nil {
		log.Fatalln(err)
	}

	// Create database table
	if _, err := db.Exec(`
		CREATE TABLE IF NOT EXISTS kv (
			project STRING,
			key STRING NOT NULL,
			val STRING NOT NULL,
			set_by STRING,
			UNIQUE (project, key) /* this should automatically index these fields */
		);
	`); err != nil {
		log.Fatalln(err)
	}

	// Create HTTP server
	r := chi.NewRouter()
	r.Use(cors.New(cors.Options{
		AllowedOrigins:   []string{"*"}, // Allow all origins
		AllowedMethods:   []string{"GET", "POST", "DELETE", "OPTIONS"},
		AllowedHeaders:   []string{"*"}, // Allow all headers
		AllowCredentials: true,
		MaxAge:           300, // Max cache age (seconds)
	}).Handler)
	r.Use(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			totalRequests++
			next.ServeHTTP(w, r)
		})
	})
	r.Get("/", func(w http.ResponseWriter, r *http.Request) {
		jsonified, err := json.Marshal(struct {
			Online   bool `json:"online"`
			ReqCount int  `json:"reqCount"`
		}{
			Online:   true,
			ReqCount: totalRequests,
		})
		if err != nil {
			log.Println(err)
			jsonError(w, "InternalServerError", http.StatusInternalServerError)
			return
		} else {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			w.Write(jsonified)
		}
	})
	r.Get("/get", func(w http.ResponseWriter, r *http.Request) {
		if !r.URL.Query().Has("key") {
			jsonError(w, "NoKeySpecified", http.StatusBadRequest)
			return
		}

		project := r.URL.Query().Get("project")
		key := r.URL.Query().Get("key")

		var val string
		err := db.QueryRow("SELECT val FROM kv WHERE project=$1 AND key=$2", project, key).Scan(&val)
		if err == sql.ErrNoRows {
			jsonError(w, "KeyFileNonExistent", http.StatusBadRequest)
			return
		} else if err != nil {
			log.Println(err)
			jsonError(w, "InternalServerError", http.StatusInternalServerError)
			return
		} else {
			w.Header().Set("Content-Type", "text/plain")
			w.WriteHeader(http.StatusOK)
			w.Write([]byte(val))
		}
	})
	r.Post("/set", func(w http.ResponseWriter, r *http.Request) {
		ip := r.Header.Get("Cf-Connecting-Ip")
		if ip == "" {
			ip, _, err = net.SplitHostPort(r.RemoteAddr)
			if err != nil {
				log.Println(err)
				jsonError(w, "InternalServerError", http.StatusForbidden)
				return
			}
		}

		timedOutUntil := timedOutIPs[ip]
		if timedOutUntil > time.Now().UnixMilli() {
			jsonError(w, "TimedOut", http.StatusForbidden)
			return
		}

		if !r.URL.Query().Has("key") {
			jsonError(w, "NoKeySpecified", http.StatusBadRequest)
			return
		}

		project := r.URL.Query().Get("project")
		key := r.URL.Query().Get("key")

		var data struct {
			Val string `json:"value"`
		}
		err = json.NewDecoder(r.Body).Decode(&data)
		if err != nil {
			jsonError(w, "InvalidBody", http.StatusBadRequest)
			return
		}

		if includesFile(data.Val) {
			timedOutIPs[ip] = time.Now().UnixMilli() + 10000 // 10 seconds
			jsonError(w, "IncludesFile", http.StatusForbidden)
			return
		}

		_, err = db.Exec("INSERT OR REPLACE INTO kv VALUES ($1, $2, $3, $4)", project, key, data.Val, ip)
		if err != nil {
			log.Println(err)
			jsonError(w, "InternalServerError", http.StatusInternalServerError)
		} else {
			jsonSuccess(w)
		}
	})
	r.Delete("/delete", func(w http.ResponseWriter, r *http.Request) {
		if !r.URL.Query().Has("key") {
			jsonError(w, "NoKeySpecified", http.StatusBadRequest)
			return
		}

		project := r.URL.Query().Get("project")
		key := r.URL.Query().Get("key")

		_, err := db.Exec("DELETE FROM kv WHERE project=$1 AND key=$2", project, key)
		if err != nil {
			log.Println(err)
			jsonError(w, "InternalServerError", http.StatusInternalServerError)
		} else {
			jsonSuccess(w)
		}
	})

	// Serve HTTP server
	port := os.Getenv("PORT")
	if port == "" {
		port = "3000"
	}
	http.ListenAndServe(":"+port, r)
}
