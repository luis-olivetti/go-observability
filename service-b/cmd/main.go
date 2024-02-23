package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	neturl "net/url"
	"time"

	"github.com/spf13/viper"
)

type ViaCep struct {
	Cep         string `json:"cep"`
	Logradouro  string `json:"logradouro"`
	Complemento string `json:"complemento"`
	Bairro      string `json:"bairro"`
	Localidade  string `json:"localidade"`
	Uf          string `json:"uf"`
	Ibge        string `json:"ibge"`
	Gia         string `json:"gia"`
	Ddd         string `json:"ddd"`
	Siafi       string `json:"siafi"`
}

type Weather struct {
	Location struct {
		Name           string  `json:"name"`
		Region         string  `json:"region"`
		Country        string  `json:"country"`
		Lat            float64 `json:"lat"`
		Lon            float64 `json:"lon"`
		TzID           string  `json:"tz_id"`
		LocaltimeEpoch int     `json:"localtime_epoch"`
		Localtime      string  `json:"localtime"`
	} `json:"location"`
	Current struct {
		TempC     float64 `json:"temp_c"`
		Condition struct {
		} `json:"condition"`
	} `json:"current"`
}

type Temperature struct {
	Celsius    float64 `json:"temp_C"`
	Fahrenheit float64 `json:"temp_F"`
	Kelvin     float64 `json:"temp_K"`
	CityName   string  `json:"city"`
}

func init() {
	// remover isso depois pois vai usar o dockerfile
	viper.SetConfigFile(".env")
	viper.ReadInConfig()

	viper.AutomaticEnv()
}

func main() {
	mux := http.NewServeMux()

	srv := &http.Server{
		Addr:         ":" + viper.GetString("PORT"),
		Handler:      mux,
		ReadTimeout:  5 * time.Second,
		WriteTimeout: 5 * time.Second,
	}

	mux.HandleFunc("/city-weather", cityWeatherHandler)

	log.Fatal(srv.ListenAndServe())
}

func getViaCep(zipCode string, w http.ResponseWriter, r *http.Request) *ViaCep {
	var response ViaCep

	url := fmt.Sprintf("http://viacep.com.br/ws/%s/json/", zipCode)

	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		http.Error(w, fmt.Sprintf("Failed to create request (viacep): %v", err), http.StatusInternalServerError)
		return nil
	}

	res, err := http.DefaultClient.Do(req)
	if err != nil {
		http.Error(w, fmt.Sprintf("Failed to make HTTP request (viacep): %v", err), http.StatusInternalServerError)
		return nil
	}
	defer res.Body.Close()

	if res.StatusCode != http.StatusOK {
		http.Error(w, fmt.Sprintf("Unexpected status code (viacep): %d", res.StatusCode), http.StatusInternalServerError)
		return nil
	}

	err = json.NewDecoder(res.Body).Decode(&response)
	if err != nil {
		http.Error(w, fmt.Sprintf("Failed to decode response (viacep): %v", err), http.StatusInternalServerError)
		return nil
	}

	return &response
}

func getWeather(cityName string, w http.ResponseWriter, r *http.Request) *Weather {
	var response Weather

	cityNameEncoded := neturl.QueryEscape(cityName)
	url := fmt.Sprintf("http://api.weatherapi.com/v1/current.json?key=a91eb948a337442782b123810242601&q=%s", cityNameEncoded)

	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		http.Error(w, fmt.Sprintf("Failed to create request (weather): %v", err), http.StatusInternalServerError)
		return nil
	}

	res, err := http.DefaultClient.Do(req)
	if err != nil {
		http.Error(w, fmt.Sprintf("Failed to make HTTP request (weather): %v", err), http.StatusInternalServerError)
		return nil
	}
	defer res.Body.Close()

	if res.StatusCode != http.StatusOK {
		http.Error(w, fmt.Sprintf("Unexpected status code (weather): %d", res.StatusCode), http.StatusInternalServerError)
		return nil
	}

	err = json.NewDecoder(res.Body).Decode(&response)
	if err != nil {
		http.Error(w, fmt.Sprintf("Failed to decode response (weather): %v", err), http.StatusInternalServerError)
		return nil
	}

	return &response
}

func cityWeatherHandler(w http.ResponseWriter, r *http.Request) {
	zipCode := r.URL.Query().Get("zipcode")

	viacep := getViaCep(zipCode, w, r)
	weather := getWeather(viacep.Localidade, w, r)

	temperature := Temperature{
		Celsius:    weather.Current.TempC,
		Fahrenheit: (weather.Current.TempC * 9 / 5) + 32,
		Kelvin:     weather.Current.TempC + 273.15,
		CityName:   viacep.Localidade,
	}

	// Responder com os dados obtidos
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(temperature)
}
