type Constructor<T = {}> = new (...args: any[]) => T;

export interface Middleware {
  pre?(messages: Record<string, any>[], context: any): Promise<Record<string, any>[]> | Record<string, any>[];
  post?(message: Record<string, any>, context: any): Promise<Record<string, any>> | Record<string, any>;
}

export function HasMiddleware<TBase extends Constructor>(Base: TBase) {
  return class extends Base {
    _middleware: Middleware[] = [];

    use(middleware: Middleware): void {
      this._middleware.push(middleware);
    }

    async _run_pre(messages: Record<string, any>[], context: any): Promise<Record<string, any>[]> {
      for (const mw of this._middleware) {
        if (mw.pre) {
          messages = await Promise.resolve(mw.pre(messages, context));
        }
      }
      return messages;
    }

    async _run_post(message: Record<string, any>, context: any): Promise<Record<string, any>> {
      for (const mw of this._middleware) {
        if (mw.post) {
          message = await Promise.resolve(mw.post(message, context));
        }
      }
      return message;
    }
  };
}
