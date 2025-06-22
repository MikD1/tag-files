using MediatR;
using Microsoft.EntityFrameworkCore;
using TagFilesService.Infrastructure;
using TagFilesService.Library.Contracts;

namespace TagFilesService.Library.Handlers;

public class GetTagsStatisticsHandler(AppDbContext dbContext)
    : IRequestHandler<GetTagsStatisticsRequest, List<TagStatisticsDto>>
{
    public async Task<List<TagStatisticsDto>> Handle(GetTagsStatisticsRequest request,
        CancellationToken cancellationToken)
    {
        FormattableString sql =
            $"""
                 SELECT t.Name AS TagName, COUNT(ft.LibraryItemId) AS UsageCount
                 FROM Tags t
                 LEFT JOIN LibraryItemTag ft ON t.Id = ft.TagsId
                 GROUP BY t.Name
                 ORDER BY UsageCount DESC, TagName ASC
             """;

        return await dbContext.Database.SqlQuery<TagStatisticsDto>(sql).ToListAsync(cancellationToken);
    }
}