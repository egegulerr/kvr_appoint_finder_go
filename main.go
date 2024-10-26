package main

import (
	"encoding/json"
	"fmt"
	"golang.org/x/net/html"
	"io"
	"log"
	"net/http"
	"regexp"
	"strings"
)

func main() {
	initialURL := "https://terminvereinbarung.muenchen.de/abh/termin/?cts=1000113"
	resp, err := http.Get(initialURL)

	if err != nil {
		log.Fatalf("Error getting inital url: %v", err)
	}

	defer resp.Body.Close()

	token, err := extractToken(resp.Body)
	if err != nil {
		log.Fatalf("Error extracting token: %v", err)
	}
	fmt.Printf("Extracted token: %s\n", token)

	// STEP 2: POST request with token
	postURL := "https://terminvereinbarung.muenchen.de/abh/termin/index.php?cts=1000113"
	formData := fmt.Sprintf("FRM_CASETYPES_token=%s&step=WEB_APPOINT_SEARCH_BY_CASETYPES&CASETYPES%%5BNotfalltermin+UA+35%%5D=1", token)

	// TODO Why not to use pointers here ?
	req, err := http.NewRequest("POST", postURL, strings.NewReader(formData))
	if err != nil {
		log.Fatalf("Error creating post request: %v", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	// TODO WHY HERE POINTER IS USED ?
	client := &http.Client{}
	resp, err = client.Do(req)

	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)

	if err != nil {
		log.Fatalf("Error sending post request: %v", err)
	}

	jsonData, err := extractJsonFromScript(string(body))
	if err != nil {
		log.Fatalf("Error extracting JSON from script: %v", err)
	}

	fmt.Printf("Extracted JSON: %s\n", jsonData)

	// TODO Find Better Naming
	found := checkAppointments(jsonData)

	if found {
		fmt.Println("Appointments found")
	} else {
		fmt.Println("No appointments found")
	}
}

// TODO Implement better version of this function
func extractToken(body io.Reader) (string, error) {
	token := ""
	tokenizedHtml := html.NewTokenizer(body)

	for {
		tt := tokenizedHtml.Next()
		switch tt {
		case html.ErrorToken:
			return "", fmt.Errorf("Token extraction failed. Token not found")

		case html.StartTagToken:
			t := tokenizedHtml.Token()
			if t.Data == "input" {
				for _, attribute := range t.Attr {
					if attribute.Key == "name" && attribute.Val == "FRM_CASETYPES_token" {
						for _, v := range t.Attr {
							if v.Key == "value" {
								token = v.Val
								return token, nil
							}
						}
					}
				}
			}
		default:
			panic("unhandled default case")
		}
	}
}

func extractJsonFromScript(body string) (string, error) {
	re := regexp.MustCompile(`var jsonAppoints = '(.*?)'`)
	match := re.FindStringSubmatch(body)

	if len(match) < 2 {
		return "", fmt.Errorf("Error extracting JSON from script")
	}
	return match[1], nil
}

func checkAppointments(jsonData string) bool {
	var appointData map[string]interface{}

	err := json.Unmarshal([]byte(jsonData), &appointData)
	if err != nil {
		log.Fatalf("Error unmarshalling JSON: %v", err)
	}
	// TODO map string olayina bak.
	loadBalancer := appointData["LOADBALANCER"].(map[string]interface{})
	appoints := loadBalancer["appoints"].(map[string]interface{})

	for date, slots := range appoints {
		if len(slots.([]interface{})) > 0 {
			fmt.Printf("Date: %s\n", date)
			return true
		}
	}
	return false
}
