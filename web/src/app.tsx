import { Route, Switch } from 'wouter'
import { Overview } from './pages/Overview'
import { NotFound } from './pages/NotFound'

export function App() {
  return (
    <Switch>
      <Route path="/" component={Overview} />
      <Route component={NotFound} />
    </Switch>
  )
}
