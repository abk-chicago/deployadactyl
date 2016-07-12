package service_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"io/ioutil"
	"net/http"
	"net/http/httptest"
	"os"

	"github.com/compozed/deployadactyl"
	"github.com/compozed/deployadactyl/artifetcher"
	"github.com/compozed/deployadactyl/artifetcher/extractor"
	"github.com/compozed/deployadactyl/config"
	"github.com/compozed/deployadactyl/deployer"
	"github.com/compozed/deployadactyl/deployer/bluegreen"
	"github.com/compozed/deployadactyl/deployer/bluegreen/pusher"
	"github.com/compozed/deployadactyl/deployer/eventmanager"
	I "github.com/compozed/deployadactyl/interfaces"
	"github.com/compozed/deployadactyl/logger"
	"github.com/compozed/deployadactyl/randomizer"
	"github.com/compozed/deployadactyl/test/mocks"
	"github.com/gin-gonic/gin"
	"github.com/go-errors/errors"
	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
	"github.com/op/go-logging"
	"github.com/spf13/afero"
	"github.com/stretchr/testify/mock"
)

const (
	ENDPOINT        = "/v1/apps/:environment/:org/:space/:appName"
	CONFIGPATH      = "./test_config.yml"
	ENVIRONMENTNAME = "test"
	TESTCONFIG      = `---
environments:
- name: Test
  domain: test.example.com
  skip_ssl: true
  foundations:
  - api1.example.com
  - api2.example.com
  - api3.example.com
  - api4.example.com
`
)

var _ = Describe("Service", func() {

	var (
		err                 error
		deployadactylServer *httptest.Server
		artifactServer      *httptest.Server
		creator             Creator
		org                 = randomizer.StringRunes(10)
		space               = randomizer.StringRunes(10)
		appName             = randomizer.StringRunes(10)
		userID              = randomizer.StringRunes(10)
		group               = randomizer.StringRunes(10)
	)

	BeforeEach(func() {
		Expect(ioutil.WriteFile(CONFIGPATH, []byte(TESTCONFIG), 0644)).To(Succeed())

		creator, err = New("debug", CONFIGPATH)
		Expect(err).ToNot(HaveOccurred())

		deployadactylHandler := creator.CreateDeployadactylHandler()

		deployadactylServer = httptest.NewServer(deployadactylHandler)

		artifactServer = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			http.ServeFile(w, r, "fixtures/artifact-with-manifest.jar")
		}))
	})

	AfterEach(func() {
		Expect(os.Remove(CONFIGPATH)).To(Succeed())
		deployadactylServer.Close()
		artifactServer.Close()
	})

	It("can deploy", func() {
		j, err := json.Marshal(gin.H{
			"artifact_url": artifactServer.URL,
			"body": gin.H{
				"user_id": userID,
				"group":   group,
			},
		})
		Expect(err).ToNot(HaveOccurred())
		jsonBuffer := bytes.NewBuffer(j)

		requestURL := fmt.Sprintf("%s/v1/apps/%s/%s/%s/%s", deployadactylServer.URL, ENVIRONMENTNAME, org, space, appName)
		req, err := http.NewRequest("POST", requestURL, jsonBuffer)
		Expect(err).ToNot(HaveOccurred())

		client := &http.Client{}
		resp, err := client.Do(req)
		Expect(err).ToNot(HaveOccurred())

		responseBody, err := ioutil.ReadAll(resp.Body)
		Expect(err).ToNot(HaveOccurred())

		Expect(resp.StatusCode).To(Equal(http.StatusOK), string(responseBody))
	})
})

type Creator struct {
	config       config.Config
	eventManager I.EventManager
	logger       *logging.Logger
	writer       io.Writer
}

func New(level string, configFilename string) (Creator, error) {
	cfg, err := config.Custom(os.Getenv, configFilename)
	if err != nil {
		return Creator{}, err
	}

	eventManager := eventmanager.NewEventManager()

	l, err := getLevel(level)
	if err != nil {
		return Creator{}, err
	}

	logger := logger.DefaultLogger(GinkgoWriter, l, "deployadactyl")
	return Creator{
		config:       cfg,
		eventManager: eventManager,
		logger:       logger,
		writer:       GinkgoWriter,
	}, nil
}

func (c Creator) CreateDeployadactylHandler() *gin.Engine {
	d := c.CreateDeployadactyl()

	r := gin.New()
	r.Use(gin.Recovery())
	r.Use(gin.LoggerWithWriter(c.CreateWriter()))
	r.Use(gin.ErrorLogger())

	r.POST(ENDPOINT, d.Deploy)

	return r
}

func (c Creator) CreateDeployadactyl() deployadactyl.Deployadactyl {
	return deployadactyl.Deployadactyl{
		Deployer:     c.CreateDeployer(),
		Log:          c.CreateLogger(),
		Config:       c.CreateConfig(),
		EventManager: c.CreateEventManager(),
		Randomizer:   c.CreateRandomizer(),
	}
}

func (c Creator) CreateRandomizer() I.Randomizer {
	return randomizer.Randomizer{}
}

func (c Creator) CreateDeployer() I.Deployer {
	return deployer.Deployer{
		Environments: c.config.Environments,
		BlueGreener:  c.CreateBlueGreener(),
		Fetcher: &artifetcher.Artifetcher{
			FileSystem: &afero.Afero{Fs: afero.NewOsFs()},
			Extractor: &extractor.Extractor{
				Log:        c.CreateLogger(),
				FileSystem: &afero.Afero{Fs: afero.NewOsFs()},
			},
			Log: c.CreateLogger(),
		},
		Prechecker:   c.CreatePrechecker(),
		EventManager: c.CreateEventManager(),
		Log:          c.CreateLogger(),
	}
}

func (c Creator) CreatePusher() (I.Pusher, error) {
	courier := &mocks.Courier{}

	courier.On("Login", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return([]byte("logged in"), nil)
	courier.On("Delete", mock.Anything).Return([]byte("deleted app"), nil)
	courier.On("Push", mock.Anything, mock.Anything).Return([]byte("pushed app"), nil)
	courier.On("Rename", mock.Anything, mock.Anything).Return([]byte("renamed app"), nil)
	courier.On("MapRoute", mock.Anything, mock.Anything).Return([]byte("mapped route"), nil)
	courier.On("CleanUp").Return(nil)

	p := pusher.Pusher{
		Courier: courier,
		Log:     c.CreateLogger(),
	}

	return p, nil
}

func (c Creator) CreateEventManager() I.EventManager {
	return c.eventManager
}

func (c Creator) CreateLogger() *logging.Logger {
	return c.logger
}

func (c Creator) CreateConfig() config.Config {
	return c.config
}

func (c Creator) CreatePrechecker() I.Prechecker {
	p := &mocks.Prechecker{}
	p.On("AssertAllFoundationsUp", mock.Anything).Return(nil)
	return p
}

func (c Creator) CreateWriter() io.Writer {
	return c.writer
}

func (c Creator) CreateBlueGreener() I.BlueGreener {
	return bluegreen.BlueGreen{
		PusherCreator: c,
		Log:           c.CreateLogger(),
	}
}

func getLevel(level string) (logging.Level, error) {
	if level != "" {
		l, err := logging.LogLevel(level)
		if err != nil {
			return 0, errors.Errorf("unable to get log level: %s. error: %s", level, err.Error())
		}
		return l, nil
	}

	return logging.INFO, nil
}
