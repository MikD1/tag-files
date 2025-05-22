using MediatR;
using Microsoft.AspNetCore.Mvc;
using TagFilesService.FilesProcessing.Contracts;

namespace TagFilesService.WebHost.Controllers;

[ApiController]
[Route("api/files-processing")]
public class FilesProcessingController(IMediator mediator) : ControllerBase
{
    [HttpGet("files")]
    public async Task<ActionResult<List<ProcessingFileDto>>> GetProcessingFiles()
    {
        GetProcessingFilesRequest request = new();
        List<ProcessingFileDto> result = await mediator.Send(request);
        return Ok(result);
    }

    [HttpPost("start-file-processing")]
    public async Task<ActionResult> StartFileProcessing(FileProcessingRequest request)
    {
        await mediator.Send(request);
        return Ok();
    }
}