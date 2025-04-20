using Microsoft.AspNetCore.Mvc;
using TagFilesService.Model;

namespace TagFilesService.WebHost.Controllers;

[ApiController]
[Route("api/tags")]
public class TagsController(ITagsRepository tagsRepository) : ControllerBase
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
}