package html

import "fmt"

func RenderLayout(title, body string) string {
	return fmt.Sprintf("<!doctype html><html><head><meta charset=\"utf-8\"><title>%s</title><link rel=\"stylesheet\" href=\"/assets/app.css\"></head><body>%s</body></html>", title, body)
}
