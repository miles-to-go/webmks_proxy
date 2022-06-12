package main

import (
	"context"
	"html/template"
	"log"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"strconv"
	"strings"
	"sync"

	"crypto/tls"
	"time"

	"github.com/gorilla/mux"

	"github.com/vmware/govmomi"
	"github.com/vmware/govmomi/find"
	"github.com/vmware/govmomi/vim25/soap"
)

func checkLogin(ctx context.Context, client *govmomi.Client, userInfo *url.Userinfo) {
	ok, err := client.SessionManager.UserSession(ctx)
	if err != nil {
		log.Fatal("UserSession: ", err)
	}

	if ok == nil {
		err = client.SessionManager.Login(ctx, userInfo)
		if err != nil {
			log.Fatal("SessionManager.Login: ", err)
		}
	}
}

func main() {
	var ticketHostMap sync.Map

	vCenterURL, err := soap.ParseURL(os.Getenv("VCENTER"))
	if err != nil {
		log.Fatal("ParseURL: ", err)
	}

	username := os.Getenv("VMRC_USER")
	password := os.Getenv("VMRC_PASS")
	userInfo := url.UserPassword(username, password)

	indexTemplate := template.Must(template.New("index.html").ParseFiles("templates/index.html"))
	consoleTemplate := template.Must(template.New("console.html").ParseFiles("./templates/console.html"))

	ctx := context.Background()
	vCenterURL.User = userInfo
	client, err := govmomi.NewClient(ctx, vCenterURL, true)
	if err != nil {
		log.Fatal("NewClient: ", err)
	}

	finder := find.NewFinder(client.Client, true)
	dc, err := finder.DefaultDatacenter(ctx)
	if err != nil {
		log.Fatal("NewFinder: ", err)
	}
	finder.SetDatacenter(dc)

	router := mux.NewRouter()

	router.HandleFunc("/", func(w http.ResponseWriter, req *http.Request) {
		checkLogin(ctx, client, userInfo)

		vms, err := finder.VirtualMachineList(ctx, "*")
		if err != nil {
			log.Fatal(err)
		}

		var vmNames []string
		for _, vm := range vms {
			powerState, err := vm.PowerState(ctx)
			if err != nil {
				log.Fatal("vm.PowerState: ", err)
			}
			if powerState == "poweredOn" {
				vmName, err := vm.ObjectName(ctx)
				if err != nil {
					log.Fatal("vm.ObjectName: ", err)
				}

				vmNames = append(vmNames, vmName)
			}
		}

		err = indexTemplate.Execute(w,
			struct {
				VMs []string
			}{
				vmNames,
			},
		)
		if err != nil {
			log.Fatal("indexTemplate.Execute: ", err)
		}
	}).Methods("GET")

	router.HandleFunc("/console/{vm}", func(w http.ResponseWriter, req *http.Request) {
		checkLogin(ctx, client, userInfo)

		vm, err := finder.VirtualMachine(ctx, mux.Vars(req)["vm"])
		if err != nil {
			log.Fatal(err)
		}

		ticket, err := vm.AcquireTicket(ctx, "webmks")
		if err != nil {
			log.Fatal("AcquireTicket: ", err)
		}

		host := net.JoinHostPort(ticket.Host, strconv.Itoa(int(ticket.Port)))
		ticketHostMap.Store(ticket.Ticket, host)

		err = consoleTemplate.Execute(w,
			struct {
				Name   string
				Ticket string
			}{
				username,
				ticket.Ticket,
			},
		)
		if err != nil {
			log.Fatal("consoleTemplate.Execute: ", err)
		}
	}).Methods("GET")

	router.PathPrefix("/static/").Handler(http.StripPrefix("/static/", http.FileServer(http.Dir("static/"))))

	wsProxy := &httputil.ReverseProxy{
		Director: func(req *http.Request) {
			host, _ := ticketHostMap.LoadAndDelete(strings.TrimPrefix(req.URL.Path, "/ticket/"))

			req.URL.Scheme = "https"
			req.Host = host.(string)
			req.URL.Host = host.(string)

			if _, ok := req.Header["User-Agent"]; !ok {
				req.Header.Set("User-Agent", "")
			}
		},
		Transport: &http.Transport{
			TLSClientConfig: &tls.Config{InsecureSkipVerify: true},
		},
		FlushInterval: time.Duration(100 * time.Millisecond),
	}

	router.Handle("/ticket/{ticket}", wsProxy)

	err = http.ListenAndServe(":8081", router)
	if err != nil {
		log.Fatal("ListenAndServe: ", err)
	}
}
