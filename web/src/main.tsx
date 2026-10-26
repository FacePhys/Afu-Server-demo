import { StrictMode } from 'react'
import { createRoot } from 'react-dom/client'
import './utils/time' // 先注册dayjs插件和中文语言
import App from './App'

createRoot(document.getElementById('root')!).render(
  <StrictMode>
    <App />
  </StrictMode>,
)
