import axios from 'axios'

const api = axios.create({
  baseURL: '/v1/internal',
  headers: { 'Content-Type': 'application/json' },
})

export default api
