# CrowdCast React Frontend

CrowdCast is a responsive virtual-event and infrastructure monitoring demo built with React, Vite, Recharts, and Lucide icons.

## Run locally

```bash
npm install
npm run dev
```

Open the local URL printed by Vite (normally `http://localhost:5173`).

## Project structure

```text
src/
├── components/
│   ├── MetricCard.jsx       # Reusable dashboard metric card
│   ├── Sidebar.jsx          # Main navigation and workspace sidebar
│   ├── StatusPill.jsx       # Reusable health/status indicator
│   ├── Toast.jsx             # Toast notification
│   └── Topbar.jsx            # Header and breadcrumb navigation
├── data/
│   └── dashboardData.js      # Demo chart and backend-node data
├── pages/
│   ├── AudiencePage.jsx
│   ├── InfrastructurePage.jsx
│   ├── LiveEventPage.jsx
│   └── OverviewPage.jsx
├── App.jsx                   # Shared state and page composition
├── index.css                # Original visual styling (unchanged)
└── main.jsx                 # React entry point
```

## Notes

- The UI styling and page markup have been kept as they were; this update only separates the monolithic React file into reusable components and page components.
- Interactions currently use demo/local state. Backend, Redis, Prometheus, and Grafana integration can be added later without changing the visual design.
