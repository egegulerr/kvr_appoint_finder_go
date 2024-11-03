package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"github.com/PuerkitoBio/goquery"
	"io"
	"log"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"regexp"
	"strings"
	"time"
)

type CreatedTaskResponse struct {
	ErrorId int `json:"errorId"`
	TaskId  int `json:"taskId"`
}

type SuccessSolvedTaskResponse struct {
	ErrorId  int    `json:"errorId"`
	Status   string `json:"status"`
	Solution struct {
		Token string `json:"token"`
	}
	Cost       string  `json:"cost"`
	Ip         string  `json:"ip"`
	CreateTime float64 `json:"createTime"`
	EndTime    float64 `json:"endTime"`
	SolveCount float64 `json:"solveCount"`
}

type ErrorSolvedTaskResponse struct {
	ErrorId          int    `json:"errorId"`
	ErrorCode        string `json:"errorCode"`
	ErrorDescription string `json:"errorDescription"`
}

type ProcessingSolvedTaskResponse struct {
	ErrorId int    `json:"errorId"`
	Status  string `json:"status"`
}

var client *http.Client

func init() {
	jar, _ := cookiejar.New(nil)
	client = &http.Client{
		Jar: jar,
	}
}

func main() {
	req, err := http.NewRequest("GET", "https://terminvereinbarung.muenchen.de/abh/termin/?cts=1000113", nil)
	if err != nil {
		log.Fatalf("Error creating request: %v", err)
	}

	req.Header.Set("User-Agent", "Mozilla/5.0 (Macintosh; Intel Mac OS X 10.15; rv:131.0) Gecko/20100101 Firefox/131.0")
	req.Header.Set("Host", "terminvereinbarung.muenchen.de")
	req.Header.Set("Referer", "https://stadt.muenchen.de/")

	// Send request with the client
	resp, err := client.Do(req)
	if err != nil {
		log.Fatalf("Error sending request: %v", err)
	}
	defer resp.Body.Close()

	doc, err := goquery.NewDocumentFromReader(resp.Body)
	if err != nil {
		log.Fatalf("Error parsing HTML: %v", err)
	}

	captchaKey, captchaExists := doc.Find("div.frc-captcha").Attr("data-sitekey")
	token, formTokenExists := doc.Find("input[name='FRM_CASETYPES_token']").Attr("value")
	if !formTokenExists {
		log.Fatalf("Error form token couldnt be found")
	}

	if captchaExists {
		solvedCaptchaToken, err := solveCaptcha(captchaKey)
		if err != nil {
			log.Fatalf("Error while solving captcha: %s", err)
		}

		resp, err = getAppointmentsPage(token, solvedCaptchaToken)
		if err != nil {
			log.Fatalf("Error getting appointments page: %v", err)
		}
	}

	body, err := io.ReadAll(resp.Body)
	defer resp.Body.Close()

	if err != nil {
		log.Fatalf("Error reading POST response: %v", err)
	}

	jsonData, err := extractAppointmentsJSONFromHtml(string(body))
	if err != nil {
		log.Fatalf("Error extracting JSON from script: %v", err)
	}

	found := checkAppointments(jsonData)
	if found {
		fmt.Println("Appointments found")
	} else {
		fmt.Println("No appointments found")
	}
}

func getAppointmentsPage(formToken string, captchaToken string) (*http.Response, error) {
	postURL := "https://terminvereinbarung.muenchen.de/abh/termin/index.php?cts=1000113"
	formData := createFormData(formToken, captchaToken)

	req, err := http.NewRequest("POST", postURL, strings.NewReader(formData.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("User-Agent", "Mozilla/5.0 (Macintosh; Intel Mac OS X 10.15; rv:131.0) Gecko/20100101 Firefox/131.0")
	req.Header.Set("Host", "terminvereinbarung.muenchen.de")
	req.Header.Set("Referer", "https://terminvereinbarung.muenchen.de/abh/termin/?cts=1000113")
	req.Header.Set("Origin", "https://terminvereinbarung.muenchen.de")
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}

	return resp, nil
}

func solveCaptcha(captchaKey string) (string, error) {

	payload := map[string]interface{}{
		"clientKey": "CLIENT_KEY",
		"task": map[string]interface{}{
			"type":       "FriendlyCaptchaTaskProxyless",
			"websiteURL": "https://terminvereinbarung.muenchen.de/abh/termin/?cts=1000113",
			"websiteKey": captchaKey,
		},
	}

	jsonPayloadData, err := json.Marshal(payload)
	if err != nil {
		return "", err
	}

	resp, err := client.Post("https://api.2captcha.com/createTask", "application/json", bytes.NewBuffer(jsonPayloadData))
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}
	fmt.Printf("Created captcha solver task. Response: %s\n", string(body))

	var taskResponse CreatedTaskResponse
	if err := json.Unmarshal(body, &taskResponse); err != nil {
		return "", err
	}

	fmt.Printf("Sleeping for 5 seconds to give time for captcha to be solved")

	for {
		time.Sleep(5 * time.Second)
		payload = map[string]interface{}{
			"clientKey": "CLIENT_KEY",
			"taskId":    taskResponse.TaskId,
		}
		jsonPayloadData, err = json.Marshal(payload)

		if err != nil {
			return "", err
		}
		resp, err = http.Post("https://api.2captcha.com/getTaskResult", "application/json", bytes.NewBuffer(jsonPayloadData))

		if err != nil {
			return "", err
		}
		data, err := io.ReadAll(resp.Body)
		err = resp.Body.Close()

		if err != nil {
			return "", err
		}

		parsedResponse, err := parseSolvedTaskResponse(data)
		if err != nil {
			return "", err
		}

		switch resp := parsedResponse.(type) {
		case SuccessSolvedTaskResponse:
			fmt.Printf("Captcha solved successfully. Token: %s \n", resp.Solution.Token)
			return resp.Solution.Token, nil

		case ErrorSolvedTaskResponse:
			log.Fatalf("Error solving captcha: %s", resp.ErrorDescription)
			return "", fmt.Errorf("Captcha couldnt be solved \n")

		case ProcessingSolvedTaskResponse:
			fmt.Printf("Captcha solving in progress \n")
		}
	}
}

func parseSolvedTaskResponse(data []byte) (interface{}, error) {

	var temp map[string]interface{}
	if err := json.Unmarshal(data, &temp); err != nil {
		return nil, err
	}

	if errorId, ok := temp["errorId"].(float64); ok {
		switch {
		case errorId != 0:
			var errResponse ErrorSolvedTaskResponse
			if err := json.Unmarshal(data, &errResponse); err != nil {
				return nil, err
			}
			return errResponse, nil

		case temp["status"].(string) == "ready":
			var successResponse SuccessSolvedTaskResponse
			if err := json.Unmarshal(data, &successResponse); err != nil {
				return nil, err
			}
			return successResponse, nil

		case temp["status"].(string) == "processing":
			var processingResponse ProcessingSolvedTaskResponse
			if err := json.Unmarshal(data, &processingResponse); err != nil {
				return nil, err
			}
			return processingResponse, nil
		}
	}
	return nil, fmt.Errorf("unknown solved task response format")
}

func createFormData(formToken string, captchaToken string) url.Values {
	formData := url.Values{}
	formData.Set("FRM_CASETYPES_token", formToken)
	formData.Set("step", "WEB_APPOINT_SEARCH_BY_CASETYPES")
	formData.Set("CASETYPES[Notfalltermin UA 35]", "1")
	formData.Set("frc-captcha-solution", captchaToken)
	return formData
}

func extractAppointmentsJSONFromHtml(body string) (string, error) {
	re := regexp.MustCompile(`var jsonAppoints = '(.*?)'`)
	match := re.FindStringSubmatch(body)

	if len(match) < 2 {
		return "", fmt.Errorf("error extracting JSON from script")
	}
	return match[1], nil
}

func checkAppointments(jsonData string) bool {
	var appointData map[string]interface{}
	err := json.Unmarshal([]byte(jsonData), &appointData)
	if err != nil {
		log.Fatalf("Error unmarshalling JSON: %v", err)
	}

	loadBalancer, ok := appointData["LOADBALANCER"].(map[string]interface{})
	if !ok {
		log.Fatalf("Error finding LOADBALANCER in JSON data")
	}

	appoints, ok := loadBalancer["appoints"].(map[string]interface{})
	if !ok {
		log.Fatalf("Error finding appointments in LOADBALANCER")
	}

	for date, slots := range appoints {
		if len(slots.([]interface{})) > 0 {
			fmt.Printf("Date: %s\n", date)
			return true
		}
	}
	return false
}
