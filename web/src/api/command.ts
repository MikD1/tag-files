import { api, ApiError, unwrap, type Schema } from './client';
type Sender<Body, Result> = (body: Body, operationId: string) => Promise<Result>;
interface Pending<Body, Result> { operationId: string; createdAt: number; body: Body; send: Sender<Body, Result> }
type Outcome<Result> = { state: 'completed'; result: Result } | { state: 'failed'; error: ApiError } | { state: 'absent' | 'pending' };
interface Options { uuid?: () => string; now?: () => number; lookup?: (id: string) => Promise<Schema<'Operation'>> }
const definitive = (error: unknown): error is ApiError => error instanceof ApiError && error.status >= 400 && error.status < 500 && error.status !== 408;
const uncertain = (cause?: unknown) => new Error('The previous save outcome is uncertain. Save again resolves the earlier values; later edits will not be sent if that save succeeded.', { cause });

/** One form's pending command always retains its submitted body, route and UUID. */
export class ImmutableCommand<Body, Result> {
  private pending: Pending<Body, Result> | undefined;
  private running: Promise<Result> | undefined;
  private readonly uuid: () => string;
  private readonly now: () => number;
  private readonly lookup: (id: string) => Promise<Schema<'Operation'>>;
  constructor(options: Options = {}) {
    this.uuid = options.uuid ?? (() => crypto.randomUUID());
    this.now = options.now ?? Date.now;
    this.lookup = options.lookup ?? (async (id) => unwrap(await api.GET('/api/v1/operations/{id}', { params: { path: { id } } })));
  }
  submit(body: Body, send: Sender<Body, Result>): Promise<Result> {
    if (this.running) return this.running;
    const submitted = structuredClone(body);
    this.running = this.execute(submitted, send).finally(() => { this.running = undefined; });
    return this.running;
  }
  private async execute(body: Body, send: Sender<Body, Result>): Promise<Result> {
    if (this.pending) {
      const prior = this.pending;
      let outcome = await this.resolve(prior);
      if (outcome.state === 'absent') {
        // A404 is not proof that an earlier HTTP request could not still commit.
        // Replaying this exact tuple lets the server's operation boundary decide.
        if (this.now() - prior.createdAt > 6 * 86400000) throw new Error('The previous save is older than operation retention and needs review. No replacement was submitted.');
        outcome = await this.dispatch(prior);
      }
      if (outcome.state === 'completed') { this.pending = undefined; return outcome.result; }
      if (outcome.state !== 'failed') throw uncertain();
      // Only a definitive failed outcome permits a fresh command for this save.
      this.pending = undefined;
    }
    const command: Pending<Body, Result> = { body, send, operationId: this.uuid(), createdAt: this.now() };
    this.pending = command;
    const outcome = await this.dispatch(command);
    if (outcome.state === 'completed') { this.pending = undefined; return outcome.result; }
    if (outcome.state === 'failed') { this.pending = undefined; throw outcome.error; }
    throw uncertain();
  }
  private async dispatch(command: Pending<Body, Result>): Promise<Outcome<Result>> {
    try { return { state: 'completed', result: await command.send(structuredClone(command.body), command.operationId) }; }
    catch (error) {
      if (definitive(error)) return { state: 'failed', error };
      return this.resolve(command);
    }
  }
  private async resolve(command: Pending<Body, Result>): Promise<Outcome<Result>> {
    let operation: Schema<'Operation'>;
    try { operation = await this.lookup(command.operationId); }
    catch (error) {
      if (error instanceof ApiError && error.status === 404) return { state: 'absent' };
      throw uncertain(error);
    }
    if (operation.state === 'failed' || operation.httpStatus >= 400) return { state: 'failed', error: new ApiError(new Response(null, { status: operation.httpStatus }), operation.result) };
    if (operation.state === 'completed' && operation.result !== null) return { state: 'completed', result: operation.result as Result };
    return { state: 'pending' };
  }
}
