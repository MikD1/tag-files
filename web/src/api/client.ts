import createClient from 'openapi-fetch';
import type {components,paths} from './schema';

export type Schema<K extends keyof components['schemas']> = components['schemas'][K];
export const api=createClient<paths>({baseUrl:globalThis.location?.origin??'http://localhost',fetch:(request)=>globalThis.fetch(request)});
export class ApiError extends Error {
 readonly status:number;
 readonly problem:Schema<'Problem'>;
 constructor(response:Response,error:unknown){
  const candidate=error as Partial<Schema<'Problem'>>|undefined;
  super(candidate?.detail ?? candidate?.title ?? `Request failed (${response.status})`);
  this.status=response.status;
  this.problem={type:'about:blank',title:this.message,status:this.status,code:'request_failed',traceId:'',...candidate};
 }
}
export function unwrap<T>(result:{data?:T;error?:unknown;response:Response}):T {
 if(!result.response.ok)throw new ApiError(result.response,result.error);
 return result.data as T;
}
export function errorMessage(error:unknown):string {
 return error instanceof Error?error.message:'The request could not be completed. Try again.';
}
