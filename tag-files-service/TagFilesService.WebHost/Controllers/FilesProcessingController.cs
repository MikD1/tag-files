using MediatR;
using Microsoft.AspNetCore.Mvc;
using TagFilesService.FilesProcessing.Contracts;

namespace TagFilesService.WebHost.Controllers;

[ApiController]
[Route("api/files-processing")]
public class FilesProcessingController(IMediator mediator) : ControllerBase
{
    [HttpPost("file-processing")]
    public async Task<IActionResult> ConvertVideo(FileProcessingRequest request)
    {
        await mediator.Send(request);
        return Ok();
    }
}