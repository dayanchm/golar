package scaffold

import (
	"encoding/json"
)

func renderFrontend(files map[string][]byte, d data) error {
	dependencies := map[string]string{}
	development := map[string]string{"vite": "8.3.4"}
	var config, main, component string
	switch d.Frontend {
	case "react":
		dependencies["react"] = "19.3.0"
		dependencies["react-dom"] = "19.3.0"
		development["@vitejs/plugin-react"] = "6.1.2"
		config = `import { defineConfig } from 'vite';
import react from '@vitejs/plugin-react';
export default defineConfig({ plugins: [react()], server: { proxy: { '/api': process.env.GOLAR_BACKEND_URL || 'http://localhost:8080', '/health': process.env.GOLAR_BACKEND_URL || 'http://localhost:8080' } } });
`
		main = `import React from 'react';
import { createRoot } from 'react-dom/client';
import App from './App.jsx';
import './style.css';
createRoot(document.getElementById('app')).render(<App />);
`
		component = `import { useState } from 'react';
export default function App() {
 const [message, setMessage] = useState('Your Golar frontend is ready.');
 async function greet() {
  try {
   const response = await fetch('/api/hello');
   if (!response.ok) throw new Error(await response.text());
   setMessage((await response.json()).message);
  } catch (error) { setMessage(error.message); }
 }
 return <main><h1>Golar + React</h1><p>{message}</p><button onClick={greet}>Call API</button></main>;
}
`
		files["frontend/src/App.jsx"] = []byte(component)
		files["frontend/src/main.jsx"] = []byte(main)
	case "vue":
		dependencies["vue"] = "3.5.43"
		development["@vitejs/plugin-vue"] = "6.0.9"
		config = `import { defineConfig } from 'vite';
import vue from '@vitejs/plugin-vue';
export default defineConfig({ plugins: [vue()], server: { proxy: { '/api': process.env.GOLAR_BACKEND_URL || 'http://localhost:8080', '/health': process.env.GOLAR_BACKEND_URL || 'http://localhost:8080' } } });
`
		main = `import { createApp } from 'vue';
import App from './App.vue';
import './style.css';
createApp(App).mount('#app');
`
		component = `<script setup>
import { ref } from 'vue';
const message = ref('Your Golar frontend is ready.');
async function greet() {
 try {
  const response = await fetch('/api/hello');
  if (!response.ok) throw new Error(await response.text());
  message.value = (await response.json()).message;
 } catch (error) { message.value = error.message; }
}
</script>
<template><main><h1>Golar + Vue</h1><p>{{ message }}</p><button @click="greet">Call API</button></main></template>
`
		files["frontend/src/App.vue"] = []byte(component)
		files["frontend/src/main.js"] = []byte(main)
	case "svelte":
		dependencies["svelte"] = "5.57.2"
		development["@sveltejs/vite-plugin-svelte"] = "7.3.1"
		config = `import { defineConfig } from 'vite';
import { svelte } from '@sveltejs/vite-plugin-svelte';
export default defineConfig({ plugins: [svelte()], server: { proxy: { '/api': process.env.GOLAR_BACKEND_URL || 'http://localhost:8080', '/health': process.env.GOLAR_BACKEND_URL || 'http://localhost:8080' } } });
`
		main = `import { mount } from 'svelte';
import App from './App.svelte';
import './style.css';
mount(App, { target: document.getElementById('app') });
`
		component = `<script>
let message = $state('Your Golar frontend is ready.');
async function greet() {
 try {
  const response = await fetch('/api/hello');
  if (!response.ok) throw new Error(await response.text());
  message = (await response.json()).message;
 } catch (error) { message = error.message; }
}
</script>
<main><h1>Golar + Svelte</h1><p>{message}</p><button onclick={greet}>Call API</button></main>
`
		files["frontend/src/App.svelte"] = []byte(component)
		files["frontend/src/main.js"] = []byte(main)
	}
	pkg := map[string]any{"name": d.Name + "-frontend", "private": true, "version": "0.0.0", "type": "module", "engines": map[string]string{"node": "^20.19.0 || ^22.12.0 || >=24"}, "scripts": map[string]string{"dev": "vite", "build": "vite build", "preview": "vite preview"}, "dependencies": dependencies, "devDependencies": development}
	body, err := json.MarshalIndent(pkg, "", "  ")
	if err != nil {
		return err
	}
	files["frontend/package.json"] = body
	files["frontend/vite.config.js"] = []byte(config)
	entry := "/src/main.js"
	if d.Frontend == "react" {
		entry = "/src/main.jsx"
	}
	files["frontend/index.html"] = []byte(`<!doctype html><html lang="en"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width, initial-scale=1"><title>` + d.Name + `</title></head><body><div id="app"></div><script type="module" src="` + entry + `"></script></body></html>`)
	files["frontend/src/style.css"] = []byte(`body{font-family:system-ui,sans-serif;background:#f4f5f7;color:#17202a;margin:0}main{max-width:640px;margin:10vh auto;padding:2rem;background:white;border-radius:12px}button,input{font:inherit;padding:.65rem;margin:.5rem 0}label{display:block}input{display:block}button{cursor:pointer}`)
	return nil
}
