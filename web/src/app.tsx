import { Route, Switch } from 'wouter'
import { Overview } from './pages/Overview'
import { Sessions } from './pages/Sessions'
import { SessionDetail } from './pages/SessionDetail'
import { Metrics } from './pages/Metrics'
import { NotFound } from './pages/NotFound'

export function App() {
  return (
    <Switch>
      <Route path="/" component={Overview} />
      <Route path="/sessions" component={Sessions} />
      <Route path="/sessions/:id">
        {(params) => <SessionDetail id={params.id} />}
      </Route>
      <Route path="/metrics" component={Metrics} />
      <Route component={NotFound} />
    </Switch>
  )
}
