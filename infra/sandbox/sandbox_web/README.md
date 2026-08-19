# Sandbox Web

Frontend service for the sandbox management platform. It integrates with the Sandbox Control Plane API to provide template management, session management, and code execution.

## Technology stack

- **React 18.3.1** - UI library
- **TypeScript 5.8.2** - Type-safe JavaScript superset
- **Ant Design 5.26.2** - Enterprise UI design language and component library
- **Rsbuild** - build tool based on Rspack
- **react-router-dom** - Routing management
- **axios** - HTTP client
- **@monaco-editor/react** - Monaco code editor

## Project structure

```
sandbox_web/
├── public/               # Public static assets
├── src/
│   ├── apis/            # API management
│   │   ├── sessions/    # Session API
│   │   ├── templates/   # Template API
│   │   ├── executions/  # Execution API
│   │   ├── files/       # File Upload/Download API
│   │   └── health/      # Health Check API
│   ├── components/      # React components
│   │   ├── Layout/      # Layout component
│   │   ├── CodeEditor/  # Code editor
│   │   └── ...
│   ├── hooks/           # Custom hooks
│   ├── pages/           # Page entries
│   ├── styles/          # Style files
│   ├── utils/           # Utility functions
│   ├── types/           # Type definitions
│   ├── constants/       # Constant definitions
│   ├── router/          # Router configuration
│   ├── App.tsx
│   └── main.tsx
├── rsbuild.config.mts   # Rsbuild configuration
├── package.json
└── tsconfig.json
```

## Quick Start

### Requirements

- Node.js 18+
- npm or yarn

### Install dependencies

```bash
npm install
```

### Development mode

```bash
npm run dev
```

The development server starts at http://localhost:1101.

### Build the project

```bash
npm run build
```

### Code linting

```bash
npm run lint
```

## Feature modules

### Template management
- Create, edit, and delete execution environment templates
- Supports Python 3.11, Node.js 20, Java 17, and Go 1.21 runtimes
- Configure CPU, memory, disk, and other resource limits

### Session management
- Create and manage code execution sessions
- View session status in real time
- Upload/download session files
- Supports Python dependency installation

### Code execution
- Online code editor (Monaco Editor)
- Supports the Lambda Handler format
- View execution history
- Display results in real time

## API integration

The frontend service connects to `http://localhost:8000` Control Plane API through a proxy.

Main endpoints:
- `/api/v1/templates` - Template management
- `/api/v1/sessions` - Session management
- `/api/v1/executions` - Code execution

## Development conventions

### Component naming
- Use PascalCase for component names：`<MyComponent />`
- Keep file names consistent with component names

### Code conventions
- Use TypeScript for type checking
- Use Hooks in function components
- Follow the ESLint and Prettier configuration

## Design specifications

| property | value |
|------|-----|
| Primary color | #126ee3 |
| Background color | #fafafa |
| Border color | #e7edf7 |
| Border radius | 4px / 8px / 12px |
