using MediatR;
using Microsoft.AspNetCore.Mvc;
using TagFilesService.Library.Contracts;
using TagFilesService.Model;

namespace TagFilesService.WebHost.Controllers;

[ApiController]
[Route("api/tags")]
public class TagsController(ITagsRepository tagsRepository, IMediator mediator) : ControllerBase
{
    [HttpGet]
    public async Task<ActionResult<List<string>>> GetTags()
    {
        List<Tag> tags = await tagsRepository.GetTags();
        List<string> tagNames = tags.Select(t => t.Name).ToList();
        return Ok(tagNames);
    }

    [HttpPost]
    public async Task<ActionResult> PostTag([FromBody] string tagName)
    {
        Tag tag = new(tagName);
        await tagsRepository.SaveTag(tag);
        return Ok();
    }

    [HttpPut("{name}")]
    public async Task<ActionResult> PutTag(string name, [FromBody] string newName)
    {
        Tag tag = await tagsRepository.GetTag(name);
        tag.Rename(newName);
        await tagsRepository.SaveTag(tag);
        return Ok();
    }

    [HttpGet("statistics")]
    public async Task<ActionResult<List<TagStatisticsDto>>> GetStatistics()
    {
        GetTagsStatisticsRequest request = new();
        List<TagStatisticsDto> result = await mediator.Send(request);
        return result;
    }
}